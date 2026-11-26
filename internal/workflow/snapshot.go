package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/romanvolkov/flows/internal/catalog"
)

// LaunchSnapshot pins a compiled workflow, its templates, resolved result schemas
// (including transitive local $refs), child definitions, and operation versions.
type LaunchSnapshot struct {
	DefinitionID      string                     `json:"definition_id"`
	Version           string                     `json:"version"`
	Digest            string                     `json:"digest"`
	Compiled          *CompiledWorkflow          `json:"compiled"`
	Templates         map[string]string          `json:"templates,omitempty"`
	Schemas           map[string]json.RawMessage `json:"schemas,omitempty"`
	OperationVersions map[string]string          `json:"operation_versions"`
	ChildSnapshots    map[string]*LaunchSnapshot `json:"child_snapshots,omitempty"`
}

// CreateSnapshot captures all templates, transitive local schemas, child workflows,
// and catalog operation versions at launch time so later disk edits do not affect the run.
func CreateSnapshot(pkgDir string, cw *CompiledWorkflow, reg *catalog.Registry, loader WorkflowLoader) (*LaunchSnapshot, error) {
	if cw == nil || cw.Definition == nil {
		return nil, fmt.Errorf("compiled workflow is nil")
	}
	snap := &LaunchSnapshot{
		DefinitionID:      cw.Definition.ID,
		Version:           cw.Definition.Version,
		Compiled:          cw,
		Templates:         make(map[string]string),
		Schemas:           make(map[string]json.RawMessage),
		OperationVersions: make(map[string]string),
		ChildSnapshots:    make(map[string]*LaunchSnapshot),
	}

	for _, nodeID := range cw.Definition.NodeOrder {
		ns := cw.Definition.Nodes[nodeID]
		if ns.Uses != "" && reg != nil {
			op, _, err := reg.Describe(ns.Uses)
			if err != nil {
				return nil, fmt.Errorf("pin operation %q: %w", ns.Uses, err)
			}
			if !op.Enabled {
				return nil, fmt.Errorf("operation %q is disabled", ns.Uses)
			}
			snap.OperationVersions[ns.Uses] = op.Version
		}

		if tplRaw, ok := ns.With["prompt_template"].(string); ok && tplRaw != "" {
			full, err := ResolveTemplatePath(pkgDir, tplRaw)
			if err != nil {
				return nil, fmt.Errorf("node %s prompt_template: %w", nodeID, err)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, fmt.Errorf("read template %q: %w", tplRaw, err)
			}
			snap.Templates[filepath.Clean(tplRaw)] = string(data)
		}

		if port, ok := cw.Ports[nodeID]; ok && port.SchemaRef != "" {
			if err := collectTransitiveSchemas(pkgDir, port.SchemaRef, snap.Schemas); err != nil {
				return nil, fmt.Errorf("node %s schema: %w", nodeID, err)
			}
		}

		if ns.Workflow != "" && loader != nil {
			childDef, err := loader(ns.Workflow)
			if err != nil {
				return nil, fmt.Errorf("load child workflow %q: %w", ns.Workflow, err)
			}
			childCW, diags := Compile(childDef, reg, loader)
			if len(diags) > 0 {
				return nil, diags[0]
			}
			childPkgDir := filepath.Dir(childDef.File)
			childSnap, err := CreateSnapshot(childPkgDir, childCW, reg, loader)
			if err != nil {
				return nil, err
			}
			snap.ChildSnapshots[ns.Workflow] = childSnap
		}
	}

	snap.Digest = computeSnapshotDigest(snap)
	return snap, nil
}

// PinAddition resolves a newly discovered operation during live graph edits and
// pins its current version without altering already-pinned operations.
func (s *LaunchSnapshot) PinAddition(opID string, reg *catalog.Registry) error {
	if _, already := s.OperationVersions[opID]; already {
		return nil
	}
	if reg == nil {
		return fmt.Errorf("catalog is nil")
	}
	op, _, err := reg.Describe(opID)
	if err != nil {
		return err
	}
	s.OperationVersions[opID] = op.Version
	return nil
}

func collectTransitiveSchemas(pkgDir, relRef string, out map[string]json.RawMessage) error {
	clean := filepath.Clean(relRef)
	if _, seen := out[clean]; seen {
		return nil
	}
	raw, err := loadLocalSchema(pkgDir, clean)
	if err != nil {
		return err
	}
	out[clean] = raw

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	var refs []string
	findJSONRefs(doc, &refs)
	refDir := filepath.Dir(clean)
	for _, r := range refs {
		if strings.HasPrefix(r, "#") || strings.Contains(r, "://") {
			continue
		}
		target := filepath.Clean(filepath.Join(refDir, r))
		if target == ".." || strings.HasPrefix(target, ".."+string(filepath.Separator)) {
			return fmt.Errorf("transitive $ref escapes package: %q", r)
		}
		if err := collectTransitiveSchemas(pkgDir, target, out); err != nil {
			return err
		}
	}
	return nil
}

func findJSONRefs(v any, refs *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			*refs = append(*refs, ref)
		}
		for _, val := range x {
			findJSONRefs(val, refs)
		}
	case []any:
		for _, val := range x {
			findJSONRefs(val, refs)
		}
	}
}

func computeSnapshotDigest(s *LaunchSnapshot) string {
	h := sha256.New()
	h.Write([]byte(s.DefinitionID + "@" + s.Version + "\n"))
	var tplKeys []string
	for k := range s.Templates {
		tplKeys = append(tplKeys, k)
	}
	slices.Sort(tplKeys)
	for _, k := range tplKeys {
		h.Write([]byte("T:" + k + "=" + s.Templates[k] + "\n"))
	}
	var opKeys []string
	for k := range s.OperationVersions {
		opKeys = append(opKeys, k)
	}
	slices.Sort(opKeys)
	for _, k := range opKeys {
		h.Write([]byte("O:" + k + "=" + s.OperationVersions[k] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
