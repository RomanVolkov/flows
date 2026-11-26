package plugin

import (
	"context"
	"os"
	"path/filepath"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/paths"
)

// Registry manages active installed tool binaries and invokes them with
// pinned build receipts.
type Registry struct {
	home paths.Home
}

// NewRegistry returns a plugin Registry rooted at home.
func NewRegistry(home paths.Home) *Registry {
	return &Registry{home: home}
}

// ListActive returns all currently activated tools under home.State.
func (r *Registry) ListActive() ([]ActiveTool, error) {
	dir := filepath.Join(r.home.State, "installed")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ActiveTool
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if active, err := LoadActiveTool(r.home, e.Name()); err == nil {
			out = append(out, active)
		}
	}
	return out, nil
}

// InvokePinned runs an operation against either a pinned cacheKey (if non-empty)
// or the currently active build of toolID.
func (r *Registry) InvokePinned(ctx context.Context, toolID, cacheKey string, cfg InvokeConfig) (InvokeResult, error) {
	if cacheKey != "" {
		cfg.Binary = filepath.Join(r.home.State, "builds", toolID, cacheKey, "plugin")
	} else {
		active, err := LoadActiveTool(r.home, toolID)
		if err != nil {
			return InvokeResult{}, err
		}
		cfg.Binary = active.Receipt.BinaryPath
	}
	return Invoke(ctx, cfg)
}

// SyncCatalog registers all active tools into cat.
func (r *Registry) SyncCatalog(ctx context.Context, cat *catalog.Registry) error {
	actives, err := r.ListActive()
	if err != nil {
		return err
	}
	for _, a := range actives {
		m, err := Describe(ctx, a.Receipt.BinaryPath)
		if err != nil {
			continue
		}
		ops := make([]catalog.OperationDescriptor, 0, len(m.Operations))
		for _, o := range m.Operations {
			ops = append(ops, catalog.OperationDescriptor{
				ID:            o.ID,
				Version:       m.Version,
				Description:   o.Description,
				ExecutionKind: catalog.ExecPlugin,
				ReadOnly:      o.ReadOnly,
				InputSchema:   o.InputSchema,
				OutputSchema:  o.OutputSchema,
				Milestones:    o.Milestones,
				Owner:         m.Owner,
				Digest:        a.Receipt.BinaryDigest,
			})
		}
		_ = cat.RegisterPackage(catalog.PackageProvenance{
			PackageID:  a.ToolID,
			Namespace:  a.ToolID,
			Version:    m.Version,
			Owner:      m.Owner,
			Digest:     a.Receipt.BinaryDigest,
			Origin:     "user",
			SourcePath: a.SourceDir,
			Enabled:    true,
			Operations: ops,
		})
	}
	return nil
}
