package library

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ValidationReport contains parser and compiler diagnostics for one package.
type ValidationReport struct {
	Valid       bool                  `json:"valid"`
	Kind        PackageKind           `json:"kind,omitempty"`
	ID          string                `json:"id,omitempty"`
	Directory   string                `json:"directory"`
	Diagnostics []workflow.Diagnostic `json:"diagnostics"`
}

// Validate checks whether a workflow or tool package can be registered by Flows.
func Validate(home paths.Home, dir string) ValidationReport {
	absDir, err := filepath.Abs(dir)
	report := ValidationReport{Directory: dir, Diagnostics: []workflow.Diagnostic{}}
	if err != nil {
		return invalid(report, "invalid_path", err.Error())
	}
	report.Directory = absDir
	if _, _, err := collectPackageFiles(absDir); err != nil {
		return invalid(report, "invalid_package_files", err.Error())
	}
	workflowPath := filepath.Join(absDir, "workflow.yaml")
	toolPath := filepath.Join(absDir, "tool.yaml")
	if _, err := os.Stat(workflowPath); err == nil {
		report.Kind = KindWorkflow
		def, diagnostics := workflow.ParseFile(workflowPath)
		if len(diagnostics) > 0 {
			report.Diagnostics = diagnostics
			return report
		}
		report.ID = def.ID
		reg := catalog.NewRegistry()
		_ = reg.RegisterBuiltins()
		if snap, scanErr := Scan(home, nil); scanErr == nil {
			_ = PublishTools(snap, reg, map[string]bool{})
		}
		loader := func(ref string) (*workflow.Definition, error) {
			target := filepath.Join(filepath.Dir(def.File), ref)
			if filepath.Ext(target) == "" {
				target = filepath.Join(target, "workflow.yaml")
			}
			sub, diags := workflow.ParseFile(target)
			if len(diags) > 0 {
				return nil, fmt.Errorf("%s", diags[0].Message)
			}
			return sub, nil
		}
		_, report.Diagnostics = workflow.Compile(def, reg, loader)
		report.Valid = len(report.Diagnostics) == 0
		return report
	}
	if _, err := os.Stat(toolPath); err == nil {
		report.Kind = KindTool
		pkg, err := ParseToolManifest(absDir, "user", "", false)
		if err != nil {
			return invalid(report, "invalid_tool_manifest", err.Error())
		}
		report.ID = pkg.PackageID
		reg := catalog.NewRegistry()
		if err := reg.RegisterPackage(pkg); err != nil {
			return invalid(report, "invalid_tool", err.Error())
		}
		report.Valid = true
		return report
	}
	return invalid(report, "manifest_not_found", "package directory must contain workflow.yaml or tool.yaml")
}

func invalid(report ValidationReport, code, message string) ValidationReport {
	report.Valid = false
	report.Diagnostics = []workflow.Diagnostic{{
		File: report.Directory, Line: 1, Column: 1, Code: code,
		Message: message, Severity: "error",
	}}
	return report
}
