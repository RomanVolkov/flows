package library

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/workflow"
)

// IndexEntry represents a discovered workflow or tool package.
type IndexEntry struct {
	Status      string                `json:"status,omitempty"`
	Diagnostics []workflow.Diagnostic `json:"diagnostics,omitempty"`
	Kind        PackageKind           `json:"kind"`
	ID          string                `json:"id"`
	Version     string                `json:"version"`
	Owner       string                `json:"owner,omitempty"`
	Digest      string                `json:"digest"`
	Origin      string                `json:"origin"` // "user" | "external"
	Path        string                `json:"path"`
}

// ConflictInfo records a package ID appearing with different digests across roots.
type ConflictInfo struct {
	Kind       PackageKind `json:"kind"`
	ID         string      `json:"id"`
	Winner     IndexEntry  `json:"winner"`
	Conflicted IndexEntry  `json:"conflicted"`
}

// IndexSnapshot is the result of scanning user library + configured extra roots.
type IndexSnapshot struct {
	Malformed []IndexEntry   `json:"malformed,omitempty"`
	Entries   []IndexEntry   `json:"entries"`
	Conflicts []ConflictInfo `json:"conflicts,omitempty"`
}

// Scan indexes home.Workflows(), home.Tools(), and extraRoots in place.
// Missing extra roots are skipped gracefully. Tool sources are indexed even
// before build/activation.
func Scan(home paths.Home, extraRoots []string) (*IndexSnapshot, error) {
	snap := &IndexSnapshot{}
	byKey := make(map[string]IndexEntry)

	recordMalformed := func(dir, origin string, kind PackageKind, err error) {
		if kind == "" {
			switch {
			case fileExists(filepath.Join(dir, "workflow.yaml")):
				kind = KindWorkflow
			case fileExists(filepath.Join(dir, "tool.yaml")):
				kind = KindTool
			default:
				return
			}
		}
		digest := ""
		if _, hash, hashErr := collectPackageFiles(dir); hashErr == nil {
			digest = hash
		}
		snap.Malformed = append(snap.Malformed, IndexEntry{
			Kind: kind, ID: filepath.Base(dir), Path: dir, Origin: origin, Digest: digest, Status: "invalid",
			Diagnostics: []workflow.Diagnostic{{File: dir, Code: "invalid_package", Message: err.Error(), Severity: "error"}},
		})
	}

	scanRoot := func(root, origin string, kind PackageKind) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return // disappeared or unreadable root handled gracefully
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pkgDir := filepath.Join(root, e.Name())
			prev, err := Preview(home, pkgDir)
			if err != nil {
				recordMalformed(pkgDir, origin, kind, err)
				continue
			}
			entry := IndexEntry{
				Kind:    prev.Kind,
				ID:      prev.ID,
				Version: prev.Version,
				Owner:   prev.Owner,
				Digest:  prev.Digest,
				Origin:  origin,
				Path:    pkgDir,
			}
			key := string(entry.Kind) + ":" + entry.ID
			if existing, dup := byKey[key]; dup {
				if existing.Digest != entry.Digest {
					snap.Conflicts = append(snap.Conflicts, ConflictInfo{
						Kind:       entry.Kind,
						ID:         entry.ID,
						Winner:     existing,
						Conflicted: entry,
					})
				}
				continue
			}
			byKey[key] = entry
			snap.Entries = append(snap.Entries, entry)
		}
	}

	scanRoot(home.Workflows(), "user", KindWorkflow)
	scanRoot(home.Tools(), "user", KindTool)
	for _, extra := range extraRoots {
		// An extra root may itself be a package directory or contain package subdirectories.
		if fileExists(filepath.Join(extra, "workflow.yaml")) || fileExists(filepath.Join(extra, "tool.yaml")) {
			if prev, err := Preview(home, extra); err == nil {
				entry := IndexEntry{
					Kind:    prev.Kind,
					ID:      prev.ID,
					Version: prev.Version,
					Owner:   prev.Owner,
					Digest:  prev.Digest,
					Origin:  "external",
					Path:    extra,
				}
				key := string(entry.Kind) + ":" + entry.ID
				if existing, dup := byKey[key]; dup {
					if existing.Digest != entry.Digest {
						snap.Conflicts = append(snap.Conflicts, ConflictInfo{Kind: entry.Kind, ID: entry.ID, Winner: existing, Conflicted: entry})
					}
				} else {
					byKey[key] = entry
					snap.Entries = append(snap.Entries, entry)
				}
			} else {
				recordMalformed(extra, "external", "", err)
			}
			continue
		}
		scanRoot(extra, "external", "")
	}
	return snap, nil
}

// ResolveLockedRevision resolves a locked dependency directly to its immutable
// revision directory in durable state.
func ResolveLockedRevision(home paths.Home, kind PackageKind, id, digest string) (string, error) {
	revDir := RevisionPath(home, kind, id, digest)
	if !dirExists(revDir) {
		return "", fmt.Errorf("locked revision %s/%s@%s not found", kind, id, digest)
	}
	return revDir, nil
}

// PublishTools registers indexed tool packages into reg (indexed before activation/build
// with Enabled=false until activated).
func PublishTools(snap *IndexSnapshot, reg *catalog.Registry, activated map[string]bool) error {
	for _, entry := range snap.Entries {
		if entry.Kind != KindTool {
			continue
		}
		enabled := activated != nil && activated[entry.ID]
		pkg, err := ParseToolManifest(entry.Path, entry.Origin, entry.Digest, enabled)
		if err != nil {
			continue
		}
		_ = reg.RegisterPackage(pkg)
	}
	return nil
}
