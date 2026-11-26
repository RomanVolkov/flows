// Package library manages importing, staging, indexing, and revisioning
// local workflow and tool packages.
package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/workflow"
)

// PackageKind identifies whether a folder is a workflow or tool package.
type PackageKind string

const (
	KindWorkflow PackageKind = "workflow"
	KindTool     PackageKind = "tool"
)

// ImportStatus classifies an import relative to the existing user copy.
type ImportStatus string

const (
	StatusNew       ImportStatus = "new"
	StatusIdentical ImportStatus = "identical"
	StatusConflict  ImportStatus = "conflict"
)

var (
	ErrEscapingPath  = errors.New("package contains escaping or symlink path")
	ErrCaseCollision = errors.New("package contains case-colliding file paths")
	ErrConflict      = errors.New("package ID already exists with different content")
)

// PreviewResult summarizes a package folder before copying it into the user library.
type PreviewResult struct {
	Kind           PackageKind  `json:"kind"`
	ID             string       `json:"id"`
	Version        string       `json:"version"`
	Owner          string       `json:"owner,omitempty"`
	Digest         string       `json:"digest"`
	OperationIDs   []string     `json:"operation_ids,omitempty"`
	Dependencies   []string     `json:"dependencies,omitempty"`
	TargetDir      string       `json:"target_dir"`
	Status         ImportStatus `json:"status"`
	ExistingDigest string       `json:"existing_digest,omitempty"`
}

// ImportOptions controls conflict handling and test hooks.
type ImportOptions struct {
	AllowOverwrite bool
	// BeforePublish, if set, runs after staging the immutable revision and
	// before promoting the active alias, allowing tests to simulate mid-publish failure.
	BeforePublish func() error
}

// ImportReceipt records a completed import and its immutable revision path.
type ImportReceipt struct {
	Preview     PreviewResult `json:"preview"`
	RevisionDir string        `json:"revision_dir"`
	AliasPath   string        `json:"alias_path"`
	ImportedAt  int64         `json:"imported_at"`
}

// ActiveAlias records the active digest and editable path for an ID.
type ActiveAlias struct {
	Kind       PackageKind `json:"kind"`
	ID         string      `json:"id"`
	Version    string      `json:"version"`
	Digest     string      `json:"digest"`
	Editable   string      `json:"editable"`
	Revision   string      `json:"revision"`
	UpdatedAt  int64       `json:"updated_at"`
}

type toolManifestYAML struct {
	Schema      string `yaml:"schema"`
	ID          string `yaml:"id"`
	Version     string `yaml:"version"`
	Owner       string `yaml:"owner"`
	Description string `yaml:"description"`
	Help        string `yaml:"help"`
	Operations  []struct {
		ID            string          `yaml:"id"`
		Description   string          `yaml:"description"`
		Help          string          `yaml:"help"`
		ExecutionKind string          `yaml:"execution_kind"`
		ReadOnly      bool            `yaml:"read_only"`
		InputSchema   map[string]any  `yaml:"input_schema"`
		OutputSchema  map[string]any  `yaml:"output_schema"`
		Milestones    []string        `yaml:"milestones"`
		Prompts       []string        `yaml:"prompts"`
	} `yaml:"operations"`
}

// Preview inspects sourceDir (containing workflow.yaml or tool.yaml) without modifying home.
func Preview(home paths.Home, sourceDir string) (PreviewResult, error) {
	absSrc, err := filepath.Abs(sourceDir)
	if err != nil {
		return PreviewResult{}, err
	}
	files, digest, err := collectPackageFiles(absSrc)
	if err != nil {
		return PreviewResult{}, err
	}

	wfPath := filepath.Join(absSrc, "workflow.yaml")
	toolPath := filepath.Join(absSrc, "tool.yaml")

	var res PreviewResult
	res.Digest = digest

	switch {
	case fileExists(wfPath):
		def, diags := workflow.ParseFile(wfPath)
		if len(diags) > 0 {
			return PreviewResult{}, diags[0]
		}
		res.Kind = KindWorkflow
		res.ID = def.ID
		res.Version = def.Version
		res.TargetDir = filepath.Join(home.Workflows(), def.ID)
		deps := make(map[string]bool)
		for _, ns := range def.Nodes {
			if ns.Uses != "" {
				deps[ns.Uses] = true
			}
			if ns.Workflow != "" {
				deps[ns.Workflow] = true
			}
		}
		for d := range deps {
			res.Dependencies = append(res.Dependencies, d)
		}
		slices.Sort(res.Dependencies)
	case fileExists(toolPath):
		data, err := os.ReadFile(toolPath)
		if err != nil {
			return PreviewResult{}, err
		}
		var tm toolManifestYAML
		if err := yaml.Unmarshal(data, &tm); err != nil {
			return PreviewResult{}, err
		}
		if tm.ID == "" || tm.Version == "" {
			return PreviewResult{}, fmt.Errorf("tool.yaml requires id and version")
		}
		res.Kind = KindTool
		res.ID = tm.ID
		res.Version = tm.Version
		res.Owner = tm.Owner
		res.TargetDir = filepath.Join(home.Tools(), tm.ID)
		for _, op := range tm.Operations {
			res.OperationIDs = append(res.OperationIDs, op.ID)
		}
		slices.Sort(res.OperationIDs)
	default:
		return PreviewResult{}, fmt.Errorf("directory %s contains neither workflow.yaml nor tool.yaml", absSrc)
	}

	_ = files
	if info, err := os.Stat(res.TargetDir); err == nil && info.IsDir() {
		_, existingDigest, err := collectPackageFiles(res.TargetDir)
		if err == nil {
			res.ExistingDigest = existingDigest
			if existingDigest == res.Digest {
				res.Status = StatusIdentical
			} else {
				res.Status = StatusConflict
			}
			return res, nil
		}
	}
	res.Status = StatusNew
	return res, nil
}

// Import stages sourceDir into immutable revision storage and atomically promotes
// the editable user copy and active alias.
func Import(home paths.Home, sourceDir string, opts ImportOptions) (ImportReceipt, error) {
	if err := home.Init(); err != nil {
		return ImportReceipt{}, err
	}
	prev, err := Preview(home, sourceDir)
	if err != nil {
		return ImportReceipt{}, err
	}
	if prev.Status == StatusConflict && !opts.AllowOverwrite {
		return ImportReceipt{}, fmt.Errorf("%w: %s (%s != %s)", ErrConflict, prev.ID, prev.Digest[:12], prev.ExistingDigest[:12])
	}

	absSrc, _ := filepath.Abs(sourceDir)
	files, _, err := collectPackageFiles(absSrc)
	if err != nil {
		return ImportReceipt{}, err
	}

	// 1. Stage immutable revision under <State>/revisions/<kind>/<id>/<digest>.
	revDir := RevisionPath(home, prev.Kind, prev.ID, prev.Digest)
	if !dirExists(revDir) {
		tmpRev := revDir + ".staging-" + fmt.Sprintf("%d", time.Now().UnixNano())
		if err := copyFileMap(absSrc, tmpRev, files); err != nil {
			_ = os.RemoveAll(tmpRev)
			return ImportReceipt{}, err
		}
		if err := os.MkdirAll(filepath.Dir(revDir), 0700); err != nil {
			_ = os.RemoveAll(tmpRev)
			return ImportReceipt{}, err
		}
		if err := os.Rename(tmpRev, revDir); err != nil {
			_ = os.RemoveAll(tmpRev)
			return ImportReceipt{}, err
		}
	}

	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return ImportReceipt{}, err
		}
	}

	// 2. Stage editable copy and swap atomically, preserving backup on failure.
	tmpTarget := prev.TargetDir + ".staging-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := copyFileMap(absSrc, tmpTarget, files); err != nil {
		_ = os.RemoveAll(tmpTarget)
		return ImportReceipt{}, err
	}
	backup := prev.TargetDir + ".bak-" + fmt.Sprintf("%d", time.Now().UnixNano())
	hadExisting := dirExists(prev.TargetDir)
	if hadExisting {
		if err := os.Rename(prev.TargetDir, backup); err != nil {
			_ = os.RemoveAll(tmpTarget)
			return ImportReceipt{}, err
		}
	}
	if err := os.Rename(tmpTarget, prev.TargetDir); err != nil {
		if hadExisting {
			_ = os.Rename(backup, prev.TargetDir)
		}
		_ = os.RemoveAll(tmpTarget)
		return ImportReceipt{}, err
	}
	if hadExisting {
		_ = os.RemoveAll(backup)
	}

	// 3. Publish active alias.
	aliasPath := filepath.Join(home.State, "aliases", string(prev.Kind), prev.ID+".json")
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0700); err != nil {
		return ImportReceipt{}, err
	}
	alias := ActiveAlias{
		Kind:      prev.Kind,
		ID:        prev.ID,
		Version:   prev.Version,
		Digest:    prev.Digest,
		Editable:  prev.TargetDir,
		Revision:  revDir,
		UpdatedAt: time.Now().UnixNano(),
	}
	aliasRaw, _ := json.MarshalIndent(alias, "", "  ")
	if err := os.WriteFile(aliasPath, aliasRaw, 0600); err != nil {
		return ImportReceipt{}, err
	}

	return ImportReceipt{
		Preview:     prev,
		RevisionDir: revDir,
		AliasPath:   aliasPath,
		ImportedAt:  alias.UpdatedAt,
	}, nil
}

// RevisionPath returns the immutable revision directory for (kind, id, digest).
func RevisionPath(home paths.Home, kind PackageKind, id, digest string) string {
	return filepath.Join(home.State, "revisions", string(kind), id, digest)
}

func collectPackageFiles(root string) ([]string, string, error) {
	var files []string
	lowerSeen := make(map[string]string)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrEscapingPath, p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: %s", ErrEscapingPath, rel)
		}
		low := strings.ToLower(rel)
		if prev, exists := lowerSeen[low]; exists && prev != rel {
			return fmt.Errorf("%w: %s vs %s", ErrCaseCollision, prev, rel)
		}
		lowerSeen[low] = rel
		if !d.IsDir() {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	slices.Sort(files)

	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, "", err
		}
		h.Write([]byte(rel + "\x00"))
		h.Write(data)
		h.Write([]byte{0})
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

func copyFileMap(srcRoot, dstRoot string, relFiles []string) error {
	if err := os.MkdirAll(dstRoot, 0700); err != nil {
		return err
	}
	for _, rel := range relFiles {
		src := filepath.Join(srcRoot, rel)
		dst := filepath.Join(dstRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		perm := info.Mode().Perm() & 0700
		if perm == 0 {
			perm = 0600
		}
		if err := os.WriteFile(dst, data, perm); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// ParseToolManifest reads tool.yaml and converts it to a catalog.PackageProvenance.
func ParseToolManifest(dir, origin, digest string, enabled bool) (catalog.PackageProvenance, error) {
	data, err := os.ReadFile(filepath.Join(dir, "tool.yaml"))
	if err != nil {
		return catalog.PackageProvenance{}, err
	}
	var tm toolManifestYAML
	if err := yaml.Unmarshal(data, &tm); err != nil {
		return catalog.PackageProvenance{}, err
	}
	ops := make([]catalog.OperationDescriptor, 0, len(tm.Operations))
	for _, o := range tm.Operations {
		var inJSON, outJSON json.RawMessage
		if len(o.InputSchema) > 0 {
			inJSON, _ = json.Marshal(o.InputSchema)
		}
		if len(o.OutputSchema) > 0 {
			outJSON, _ = json.Marshal(o.OutputSchema)
		}
		ops = append(ops, catalog.OperationDescriptor{
			ID:            o.ID,
			Version:       tm.Version,
			Description:   o.Description,
			Help:          o.Help,
			ExecutionKind: catalog.ExecutionKind(o.ExecutionKind),
			ReadOnly:      o.ReadOnly,
			InputSchema:   inJSON,
			OutputSchema:  outJSON,
			Milestones:    o.Milestones,
			Prompts:       o.Prompts,
			Owner:         tm.Owner,
			Digest:        digest,
		})
	}
	return catalog.PackageProvenance{
		PackageID:  tm.ID,
		Namespace:  tm.ID,
		Version:    tm.Version,
		Owner:      tm.Owner,
		Digest:     digest,
		Origin:     origin,
		SourcePath: dir,
		Enabled:    enabled,
		Operations: ops,
	}, nil
}
