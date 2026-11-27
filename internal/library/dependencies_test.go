package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidationDistinguishesPackageAndDependencyReadiness(t *testing.T) {
	home := testHome(t)
	dir := t.TempDir()
	body := `schema: flows/v1
id: example
version: 1.0.0
nodes:
  one:
    uses: core.command
    with:
      command: absent-flows-fixture-executable
  two:
    uses: core.command
    with:
      command: absent-flows-fixture-executable
`
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if report := Validate(home, dir); !report.Valid {
		t.Fatalf("static validation failed: %+v", report)
	}
	report := ValidateWithDependencies(t.Context(), home, dir)
	if report.Valid || len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "missing_executable" {
		t.Fatalf("missing executable not reported once: %+v", report)
	}
}

func TestDependencyValidationChecksProviderAuthWithoutPrintingOutput(t *testing.T) {
	home := testHome(t)
	dir := t.TempDir()
	body := `schema: flows/tool/v1
id: example
version: 1.0.0
required_clis: [gh]
credentials:
  - id: github-cli
    provider: github
    required: true
operations:
  - id: example.run
    description: fixture
    execution_kind: plugin
`
	for name, content := range map[string]string{"tool.yaml": body, "example-plugin": "#!/bin/sh\n", "gh": "#!/bin/sh\necho private-test-value >&2\nexit 1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FLOWS_GH_BIN", filepath.Join(dir, "gh"))
	report := ValidateWithDependencies(t.Context(), home, dir)
	if report.Valid || len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "credential_unavailable" ||
		report.Diagnostics[0].Message != "Could not verify github authentication. Run gh auth status to check credentials and connectivity." {
		t.Fatalf("unexpected authentication report: %+v", report)
	}
}
