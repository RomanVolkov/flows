package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

// OperationItem holds expandable schema, credential, and readiness details.
type OperationItem struct {
	ID                  string             `json:"id"`
	Description         string             `json:"description"`
	InputSchema         json.RawMessage    `json:"input_schema,omitempty"`
	OutputSchema        json.RawMessage    `json:"output_schema,omitempty"`
	RequiredCredentials []string           `json:"required_credentials,omitempty"`
	State               catalog.ReadyState `json:"state"`
	Reason              string             `json:"reason,omitempty"`
	RepairHint          string             `json:"repair_hint,omitempty"`
	CheckedAt           int64              `json:"checked_at"`
}

// ToolItem represents one installed or indexed tool package in Settings -> Tools.
type ToolItem struct {
	PackageID  string          `json:"package_id"`
	Version    string          `json:"version"`
	SourcePath string          `json:"source_path"`
	Enabled    bool            `json:"enabled"`
	Expanded   bool            `json:"expanded"`
	Operations []OperationItem `json:"operations"`
	ReadyCount int             `json:"ready_count"`
	TotalCount int             `json:"total_count"`
}

// ToolInventoryPane manages library paths, extra roots, imports, and readiness.
type ToolInventoryPane struct {
	WorkflowsDir            string     `json:"workflows_dir"`
	ToolsDir                string     `json:"tools_dir"`
	SpecDir                 string     `json:"spec_dir"`
	AuthoringGuidePath      string     `json:"authoring_guide_path,omitempty"`
	AuthoringGuideInstalled bool       `json:"authoring_guide_installed"`
	ExtraRoots              []string   `json:"extra_roots,omitempty"`
	Tools                   []ToolItem `json:"tools,omitempty"`
}

// NewToolInventoryPane initializes library paths and checks for installed authoring docs.
func NewToolInventoryPane(home paths.Home, extraRoots []string) *ToolInventoryPane {
	guidePath := filepath.Join(home.Spec(), "v1", "authoring.md")
	_, err := os.Stat(guidePath)
	return &ToolInventoryPane{
		WorkflowsDir:            home.Workflows(),
		ToolsDir:                home.Tools(),
		SpecDir:                 home.Spec(),
		AuthoringGuidePath:      guidePath,
		AuthoringGuideInstalled: err == nil,
		ExtraRoots:              append([]string(nil), extraRoots...),
	}
}

// AddExtraRoot adds an external search root if not already present.
func (p *ToolInventoryPane) AddExtraRoot(root string) {
	if root == "" || slices.Contains(p.ExtraRoots, root) {
		return
	}
	p.ExtraRoots = append(p.ExtraRoots, root)
}

// RemoveExtraRoot removes an external search root.
func (p *ToolInventoryPane) RemoveExtraRoot(root string) {
	p.ExtraRoots = slices.DeleteFunc(p.ExtraRoots, func(r string) bool { return r == root })
}

// ImportPackage imports dir into home.
func (p *ToolInventoryPane) ImportPackage(home paths.Home, dir string, overwrite bool) (library.ImportReceipt, error) {
	return library.Import(home, dir, library.ImportOptions{AllowOverwrite: overwrite})
}

// RefreshReadiness evaluates specs using checker and updates per-tool and total counts.
func (p *ToolInventoryPane) RefreshReadiness(ctx context.Context, checker *catalog.ReadinessChecker, specs []catalog.ToolCheckSpec, descs map[string]catalog.OperationDescriptor) {
	p.Tools = make([]ToolItem, 0, len(specs))
	for _, spec := range specs {
		reports := checker.Check(ctx, spec)
		item := ToolItem{
			PackageID:  spec.PackageID,
			SourcePath: spec.BuildPath,
			Enabled:    spec.Enabled,
			TotalCount: len(reports),
		}
		for _, rep := range reports {
			op := OperationItem{
				ID:         rep.OperationID,
				State:      rep.State,
				Reason:     rep.Reason,
				RepairHint: rep.RepairHint,
				CheckedAt:  rep.CheckedAt,
			}
			if d, ok := descs[rep.OperationID]; ok {
				op.Description = d.Description
				op.InputSchema = d.InputSchema
				op.OutputSchema = d.OutputSchema
				item.Version = d.Version
			}
			if rep.State == catalog.ReadyOK {
				item.ReadyCount++
			}
			item.Operations = append(item.Operations, op)
		}
		p.Tools = append(p.Tools, item)
	}
}

// Totals returns (readyCount, totalCount) across all operations in the inventory.
func (p *ToolInventoryPane) Totals() (int, int) {
	ready, total := 0, 0
	for _, t := range p.Tools {
		ready += t.ReadyCount
		total += t.TotalCount
	}
	return ready, total
}
