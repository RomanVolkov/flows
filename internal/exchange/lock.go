// Package exchange exports and imports portable .flows ZIP archives with
// dependency locks, transitive schemas, and deterministic checksums.
package exchange

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

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

const BundleSchemaVersion = "flows/bundle/v1"

var (
	ErrMissingDependency = errors.New("missing bundle dependency")
	ErrCaseConflict      = errors.New("case-conflicting path in bundle")
)

// LockedPackage records a pinned workflow or tool package inside bundle.json.
type LockedPackage struct {
	Kind    string `json:"kind"` // "workflow" | "tool" | "sdk"
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Path    string `json:"path"`
}

// ExternalPrerequisites separates built-ins, harnesses, and provider accounts
// from portable bundle payloads.
type ExternalPrerequisites struct {
	Builtins         []string `json:"builtins,omitempty"`
	Harnesses        []string `json:"harnesses,omitempty"`
	ProviderAccounts []string `json:"provider_accounts,omitempty"`
	RequiredCLIs     []string `json:"required_clis,omitempty"`
}

// BundleManifest is the top-level bundle.json inside a .flows ZIP archive.
type BundleManifest struct {
	Schema                string                     `json:"schema"`
	EntryKind             string                     `json:"entry_kind"`
	EntryID               string                     `json:"entry_id"`
	EngineCompatibility   string                     `json:"engine_compatibility"`
	Packages              []LockedPackage            `json:"packages"`
	DependencyLock        map[string]string          `json:"dependency_lock"`
	PayloadChecksums      map[string]string          `json:"payload_checksums"`
	Schemas               map[string]json.RawMessage `json:"schemas,omitempty"`
	ExternalPrerequisites ExternalPrerequisites      `json:"external_prerequisites"`
	Licenses              []string                   `json:"licenses,omitempty"`
}

// ExportOptions configures .flows archive creation.
type ExportOptions struct {
	Catalog           *catalog.Registry
	ToolDirs          map[string]string // packageID -> local source dir
	CandidatePackages []string          // extra directories for dynamic discovery
	SDKDir            string            // local sdk/go dir to bundle for offline builds
	ProviderAccounts  []string          // credential ref names only (never raw secrets)
}

type archiveEntry struct {
	zipPath string
	data    []byte
	mode    fs.FileMode
}

type resolvedBundle struct {
	manifest BundleManifest
	files    []archiveEntry
}

func resolveClosure(entryDir string, opts ExportOptions) (*resolvedBundle, error) {
	absEntry, err := filepath.Abs(entryDir)
	if err != nil {
		return nil, err
	}

	wfPath := filepath.Join(absEntry, "workflow.yaml")
	def, pDiags := workflow.ParseFile(wfPath)
	if len(pDiags) > 0 {
		return nil, pDiags[0]
	}

	loader := func(ref string) (*workflow.Definition, error) {
		p := filepath.Join(absEntry, ref, "workflow.yaml")
		sub, d := workflow.ParseFile(p)
		if len(d) > 0 {
			return nil, d[0]
		}
		return sub, nil
	}
	cw, cDiags := workflow.Compile(def, opts.Catalog, loader)
	if len(cDiags) > 0 {
		return nil, cDiags[0]
	}

	// Snapshot validates that all templates and transitive local $ref schemas exist.
	snap, err := workflow.CreateSnapshot(absEntry, cw, opts.Catalog, loader)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMissingDependency, err)
	}

	seenLower := make(map[string]string)
	fileMap := make(map[string]archiveEntry)
	addDir := func(srcDir, zipPrefix string, skipLogsAndCreds bool) error {
		return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlinks not allowed in bundle: %s", p)
			}
			rel, err := filepath.Rel(srcDir, p)
			if err != nil || rel == "." {
				return err
			}
			if skipLogsAndCreds && isPrivateOrRuntimePath(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			zp := filepath.ToSlash(filepath.Join(zipPrefix, rel))
			low := strings.ToLower(zp)
			if prev, dup := seenLower[low]; dup && prev != zp {
				return fmt.Errorf("%w: %s vs %s", ErrCaseConflict, prev, zp)
			}
			seenLower[low] = zp
			if d.IsDir() {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			fileMap[zp] = archiveEntry{zipPath: zp, data: data, mode: info.Mode().Perm()}
			return nil
		})
	}

	entryZipRoot := filepath.ToSlash(filepath.Join("packages", "workflows", def.ID))
	if err := addDir(absEntry, entryZipRoot, true); err != nil {
		return nil, err
	}

	var packages []LockedPackage
	depLock := make(map[string]string)
	builtins := make(map[string]bool)
	harnesses := make(map[string]bool)
	toolsNeeded := make(map[string]bool)

	var collectPrereqs func(d *workflow.Definition)
	collectPrereqs = func(d *workflow.Definition) {
		for _, ns := range d.Nodes {
			if ns.Uses != "" {
				nsPrefix, _, _ := strings.Cut(ns.Uses, ".")
				if nsPrefix == "core" {
					builtins[ns.Uses] = true
					if ns.Uses == "core.harness" {
						if h, ok := ns.With["harness"].(string); ok && h != "" && !strings.Contains(h, "${{") {
							harnesses[h] = true
						}
					}
				} else {
					toolsNeeded[nsPrefix] = true
				}
			}
			if ns.Workflow != "" {
				if sub, err := loader(ns.Workflow); err == nil {
					collectPrereqs(sub)
				}
			}
		}
	}
	collectPrereqs(def)

	entryDigest := dirDigest(fileMap, entryZipRoot)
	packages = append(packages, LockedPackage{
		Kind: "workflow", ID: def.ID, Version: def.Version, Digest: entryDigest, Path: entryZipRoot,
	})
	depLock["workflow:"+def.ID] = entryDigest

	for toolID := range toolsNeeded {
		tDir, ok := opts.ToolDirs[toolID]
		if !ok {
			continue
		}
		tZipRoot := filepath.ToSlash(filepath.Join("packages", "tools", toolID))
		if err := addDir(tDir, tZipRoot, true); err != nil {
			return nil, err
		}
		d := dirDigest(fileMap, tZipRoot)
		packages = append(packages, LockedPackage{
			Kind: "tool", ID: toolID, Version: "1.0.0", Digest: d, Path: tZipRoot,
		})
		depLock["tool:"+toolID] = d
	}

	for _, cand := range opts.CandidatePackages {
		cZipRoot := filepath.ToSlash(filepath.Join("packages", "candidates", filepath.Base(cand)))
		if err := addDir(cand, cZipRoot, true); err != nil {
			return nil, err
		}
		d := dirDigest(fileMap, cZipRoot)
		packages = append(packages, LockedPackage{
			Kind: "candidate", ID: filepath.Base(cand), Version: "1.0.0", Digest: d, Path: cZipRoot,
		})
	}

	if opts.SDKDir != "" {
		if err := addDir(opts.SDKDir, "sdk/go", true); err != nil {
			return nil, err
		}
	}

	slices.SortFunc(packages, func(a, b LockedPackage) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})

	checksums := make(map[string]string, len(fileMap))
	files := make([]archiveEntry, 0, len(fileMap))
	for zp, e := range fileMap {
		sum := sha256.Sum256(e.data)
		checksums[zp] = hex.EncodeToString(sum[:])
		files = append(files, e)
	}
	slices.SortFunc(files, func(a, b archiveEntry) int {
		return strings.Compare(a.zipPath, b.zipPath)
	})

	manifest := BundleManifest{
		Schema:              BundleSchemaVersion,
		EntryKind:           "workflow",
		EntryID:             def.ID,
		EngineCompatibility: ">=1.0.0 <2.0.0",
		Packages:            packages,
		DependencyLock:      depLock,
		PayloadChecksums:    checksums,
		Schemas:             snap.Schemas,
		ExternalPrerequisites: ExternalPrerequisites{
			Builtins:         sortedKeys(builtins),
			Harnesses:        sortedKeys(harnesses),
			ProviderAccounts: slices.Clone(opts.ProviderAccounts),
		},
	}

	return &resolvedBundle{manifest: manifest, files: files}, nil
}

func isPrivateOrRuntimePath(rel string) bool {
	low := strings.ToLower(filepath.ToSlash(rel))
	for _, seg := range strings.Split(low, "/") {
		if seg == ".git" || seg == "logs" || seg == "credentials" || seg == "secrets" {
			return true
		}
	}
	return strings.HasSuffix(low, ".log") || strings.HasSuffix(low, ".env")
}

func dirDigest(m map[string]archiveEntry, prefix string) string {
	var paths []string
	for p := range m {
		if strings.HasPrefix(p, prefix+"/") {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p + "\x00"))
		h.Write(m[p].data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
