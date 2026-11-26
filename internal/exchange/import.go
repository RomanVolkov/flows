package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
)

// BundleImportOptions controls conflict resolution and testing hooks.
type BundleImportOptions struct {
	AllowOverwrite bool
	SideBySide     bool // keeps existing editable copy & active alias while storing new immutable revision
	Catalog        *catalog.Registry
	BeforeCommit   func() error // hook to simulate crash/restart during promotion
}

// PromotionJournal records an in-flight bundle promotion for crash recovery.
type PromotionJournal struct {
	ID         string         `json:"id"`
	Manifest   BundleManifest `json:"manifest"`
	StageDir   string         `json:"stage_dir"`
	State      string         `json:"state"` // "staged" | "committed"
	UpdatedAt  int64          `json:"updated_at"`
}

// BundleImportReceipt summarizes a completed atomic .flows import.
type BundleImportReceipt struct {
	Preview          BundlePreview `json:"preview"`
	CatalogGeneration int64        `json:"catalog_generation"`
	InstalledTools   []string      `json:"installed_tools,omitempty"`
	RevisionDirs     []string      `json:"revision_dirs"`
}

// ImportBundle stages a verified .flows archive, builds any bundled tools offline,
// journals promotion, and publishes one catalog generation.
func ImportBundle(ctx context.Context, home paths.Home, sourcePath string, opts BundleImportOptions) (BundleImportReceipt, error) {
	if err := home.Init(); err != nil {
		return BundleImportReceipt{}, err
	}
	prev, err := PreviewBundle(home, sourcePath)
	if err != nil {
		return BundleImportReceipt{}, err
	}
	if prev.Status == library.StatusConflict && !opts.AllowOverwrite && !opts.SideBySide {
		return BundleImportReceipt{}, fmt.Errorf("%w: %s", library.ErrConflict, prev.Manifest.EntryID)
	}

	stageRoot := filepath.Join(home.State, "staging", fmt.Sprintf("bundle-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(stageRoot, 0700); err != nil {
		return BundleImportReceipt{}, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stageRoot)
		}
	}()

	for rel, data := range prev.payloads {
		target := filepath.Join(stageRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return BundleImportReceipt{}, err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return BundleImportReceipt{}, err
		}
	}

	// Build any bundled tool packages before promotion (cancelable mid-build).
	var builtTools []string
	for _, pkg := range prev.Manifest.Packages {
		if pkg.Kind != "tool" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return BundleImportReceipt{}, err
		}
		toolSrc := filepath.Join(stageRoot, filepath.FromSlash(pkg.Path))
		active, err := plugin.Install(ctx, home, pkg.ID, pkg.Version, toolSrc, plugin.InstallOptions{})
		if err != nil {
			return BundleImportReceipt{}, fmt.Errorf("build bundled tool %s: %w", pkg.ID, err)
		}
		builtTools = append(builtTools, active.ToolID)
	}

	// Journal promotion before mutating revisions/editable copies.
	journalDir := filepath.Join(home.State, "journals")
	if err := os.MkdirAll(journalDir, 0700); err != nil {
		return BundleImportReceipt{}, err
	}
	jID := fmt.Sprintf("promo-%d", time.Now().UnixNano())
	journalPath := filepath.Join(journalDir, jID+".json")
	j := PromotionJournal{
		ID:        jID,
		Manifest:  prev.Manifest,
		StageDir:  stageRoot,
		State:     "staged",
		UpdatedAt: time.Now().UnixNano(),
	}
	jRaw, _ := json.MarshalIndent(j, "", "  ")
	if err := os.WriteFile(journalPath, jRaw, 0600); err != nil {
		return BundleImportReceipt{}, err
	}
	keepStage = true

	if opts.BeforeCommit != nil {
		if err := opts.BeforeCommit(); err != nil {
			return BundleImportReceipt{}, err
		}
	}

	var revDirs []string
	for _, pkg := range prev.Manifest.Packages {
		if pkg.Kind != "workflow" && pkg.Kind != "tool" {
			continue
		}
		pkgSrc := filepath.Join(stageRoot, filepath.FromSlash(pkg.Path))
		revDir := library.RevisionPath(home, library.PackageKind(pkg.Kind), pkg.ID, pkg.Digest)
		if _, err := os.Stat(revDir); os.IsNotExist(err) {
			tmpRev := revDir + ".tmp"
			_ = os.RemoveAll(tmpRev)
			if err := copyStageTree(pkgSrc, tmpRev); err != nil {
				_ = os.RemoveAll(tmpRev)
				return BundleImportReceipt{}, err
			}
			if err := os.Rename(tmpRev, revDir); err != nil {
				_ = os.RemoveAll(tmpRev)
				return BundleImportReceipt{}, err
			}
		}
		revDirs = append(revDirs, revDir)

		if pkg.Kind == "workflow" {
			targetDir := filepath.Join(home.Workflows(), pkg.ID)
			if opts.SideBySide {
				if _, err := os.Stat(targetDir); err == nil {
					// Preserve existing editable copy and active alias on side-by-side conflict.
					continue
				}
			}
			if _, err := library.Import(home, pkgSrc, library.ImportOptions{AllowOverwrite: opts.AllowOverwrite}); err != nil {
				return BundleImportReceipt{}, err
			}
		}
	}

	if opts.Catalog != nil && len(builtTools) > 0 {
		_ = plugin.NewRegistry(home).SyncCatalog(ctx, opts.Catalog)
	}

	j.State = "committed"
	j.UpdatedAt = time.Now().UnixNano()
	jDone, _ := json.MarshalIndent(j, "", "  ")
	_ = os.WriteFile(journalPath, jDone, 0600)

	return BundleImportReceipt{
		Preview:           prev,
		CatalogGeneration: j.UpdatedAt,
		InstalledTools:    builtTools,
		RevisionDirs:      revDirs,
	}, nil
}

// RecoverJournals returns any staged-but-uncommitted promotion journals after a crash.
func RecoverJournals(home paths.Home) ([]PromotionJournal, error) {
	journalDir := filepath.Join(home.State, "journals")
	entries, err := os.ReadDir(journalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pending []PromotionJournal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(journalDir, e.Name()))
		if err != nil {
			continue
		}
		var j PromotionJournal
		if json.Unmarshal(data, &j) == nil && j.State == "staged" {
			pending = append(pending, j)
		}
	}
	return pending, nil
}

func copyStageTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0600)
	})
}
