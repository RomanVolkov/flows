// Package cli implements user-facing CLI subcommands such as validate, import, spec, and doctor.
package cli

import (
	"encoding/json"
	"path/filepath"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ValidateReport is the JSON output of `flows validate DIRECTORY --json`.
type ValidateReport struct {
	Valid       bool                  `json:"valid"`
	Directory   string                `json:"directory"`
	WorkflowID  string                `json:"workflow_id,omitempty"`
	Diagnostics []workflow.Diagnostic `json:"diagnostics"`
}

// ValidateDirectory parses, type-checks, and catalog-resolves workflow.yaml in dir.
func ValidateDirectory(dir string, reg *catalog.Registry) ValidateReport {
	wfPath := filepath.Join(dir, "workflow.yaml")
	def, parseDiags := workflow.ParseFile(wfPath)
	if len(parseDiags) > 0 {
		return ValidateReport{
			Valid:       false,
			Directory:   dir,
			Diagnostics: parseDiags,
		}
	}
	loader := func(ref string) (*workflow.Definition, error) {
		target := filepath.Join(dir, ref)
		if filepath.Ext(target) == "" {
			target = filepath.Join(dir, ref, "workflow.yaml")
		}
		sub, diags := workflow.ParseFile(target)
		if len(diags) > 0 {
			return nil, diags[0]
		}
		return sub, nil
	}
	_, diags := workflow.Compile(def, reg, loader)
	return ValidateReport{
		Valid:       len(diags) == 0,
		Directory:   dir,
		WorkflowID:  def.ID,
		Diagnostics: diags,
	}
}

// FormatValidateJSON encodes report as indented JSON.
func FormatValidateJSON(report ValidateReport) ([]byte, error) {
	if report.Diagnostics == nil {
		report.Diagnostics = []workflow.Diagnostic{}
	}
	return json.MarshalIndent(report, "", "  ")
}
