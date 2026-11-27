package library

import (
	"context"
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
	return validate(context.Background(), home, dir, false)
}

// ValidateWithDependencies also checks local runtime prerequisites. It works
// without importing the package or executing any workflow nodes.
func ValidateWithDependencies(ctx context.Context, home paths.Home, dir string) ValidationReport {
	return validate(ctx, home, dir, true)
}

func validate(ctx context.Context, home paths.Home, dir string, checkDependencies bool) ValidationReport {
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
		installed := make(map[string]string)
		if snap, scanErr := Scan(home, nil); scanErr == nil {
			_ = PublishTools(snap, reg, nil)
			for _, entry := range snap.Entries {
				if entry.Kind == KindWorkflow {
					installed[entry.ID] = filepath.Join(entry.Path, "workflow.yaml")
				}
			}
		}
		loader := func(parent *workflow.Definition, ref string) (*workflow.Definition, error) {
			target, ok := installed[ref]
			if !ok {
				if !filepath.IsLocal(ref) {
					return nil, fmt.Errorf("invalid workflow reference %q", ref)
				}
				target = filepath.Join(filepath.Dir(parent.File), ref)
				if filepath.Ext(target) == "" {
					target = filepath.Join(target, "workflow.yaml")
				}
			}
			sub, diags := workflow.ParseFile(target)
			if len(diags) > 0 {
				return nil, diags[0]
			}
			return sub, nil
		}
		_, report.Diagnostics = workflow.CompileWithLoader(def, reg, loader)
		report.Valid = len(report.Diagnostics) == 0
		if report.Valid && checkDependencies {
			checker := newDependencyValidator(ctx, home, &report)
			checker.workflow(def, reg, loader)
		}
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
		if checkDependencies {
			checker := newDependencyValidator(ctx, home, &report)
			checker.tool(absDir, pkg.PackageID, false)
		}
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
