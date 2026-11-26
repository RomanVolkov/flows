package workflow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/cli"
	"github.com/romanvolkov/flows/internal/workflow"
)

func testCatalog(t *testing.T) *catalog.Registry {
	t.Helper()
	reg := catalog.NewRegistry()
	if err := reg.RegisterBuiltins(); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "github",
		Namespace: "github",
		Version:   "1.0.0",
		Origin:    "user",
		Enabled:   true,
		Operations: []catalog.OperationDescriptor{
			{ID: "github.list-pull-requests", Description: "List PRs", ExecutionKind: catalog.ExecPlugin, ReadOnly: true},
			{ID: "github.merge-pr", Description: "Merge PR", ExecutionKind: catalog.ExecPlugin, ReadOnly: false},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const reviewOpenPRsYAML = `schema: flows/v1
id: review-open-prs
version: 1.0.0
inputs:
  repository: {type: string, required: true}
  reviewer: {type: string, default: codex}
nodes:
  discover:
    uses: github.list-pull-requests
    with:
      repository: ${{ inputs.repository }}
      state: open
  review:
    uses: core.harness
    for_each:
      items: ${{ nodes.discover.outputs.pull_requests }}
      key: ${{ item.id }}
    with:
      harness: ${{ inputs.reviewer }}
      prompt_template: ./prompts/review.md
      arguments:
        pull_request: ${{ item }}
    outputs:
      approved: {type: boolean, required: true}
      findings: {type: array, items: {type: string}, required: true}
outputs:
  reviews: ${{ nodes.review.outputs }}
`

func TestCompileReviewOpenPRsAndOutputForms(t *testing.T) {
	reg := testCatalog(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(reviewOpenPRsYAML), 0600); err != nil {
		t.Fatal(err)
	}

	report := cli.ValidateDirectory(dir, reg)
	if !report.Valid || len(report.Diagnostics) != 0 {
		t.Fatalf("expected valid review-open-prs, got %+v", report.Diagnostics)
	}

	// Verify AST preservation + array output port for for_each.
	def, diags := workflow.ParseFile(filepath.Join(dir, "workflow.yaml"))
	if len(diags) > 0 || def.SyntaxTree == nil {
		t.Fatalf("expected preserved syntax tree, diags=%+v", diags)
	}
	cw, diags := workflow.Compile(def, reg, nil)
	if len(diags) > 0 {
		t.Fatalf("Compile failed: %+v", diags)
	}
	if !cw.Ports["review"].IsArray || len(cw.Ports["review"].Fields) != 2 {
		t.Fatalf("expected array port with 2 fields for review, got %+v", cw.Ports["review"])
	}

	// Test omission and standalone local $ref output forms.
	schemaPath := filepath.Join(dir, "schemas", "result.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	formsYAML := `schema: flows/v1
id: output-forms
version: 1.0.0
nodes:
  omitted:
    uses: core.harness
  reffed:
    uses: core.harness
    needs: [omitted]
    outputs:
      $ref: ./schemas/result.json
`
	def2, pDiags := workflow.ParseBytes(filepath.Join(dir, "workflow.yaml"), []byte(formsYAML))
	if len(pDiags) > 0 {
		t.Fatal(pDiags)
	}
	cw2, cDiags := workflow.Compile(def2, reg, nil)
	if len(cDiags) > 0 {
		t.Fatalf("Compile output-forms failed: %+v", cDiags)
	}
	if !cw2.Ports["omitted"].CompletionOnly || !json.Valid(cw2.Ports["reffed"].ResolvedSchema) {
		t.Fatalf("unexpected ports: %+v", cw2.Ports)
	}
}

func TestValidationErrorsAndNestedRecursion(t *testing.T) {
	reg := testCatalog(t)
	dir := t.TempDir()

	cases := []struct {
		name string
		yaml string
		code string
	}{
		{
			name: "unsupported_schema",
			yaml: "schema: flows/v99\nid: bad\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n",
			code: "unsupported_schema",
		},
		{
			name: "bad_key",
			yaml: "schema: flows/v1\nid: bad-key\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n    for_each:\n      items: x\n      key: constant\n",
			code: "invalid_key",
		},
		{
			name: "bad_edge",
			yaml: "schema: flows/v1\nid: bad-edge\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n    needs: [missing]\n",
			code: "unknown_edge",
		},
		{
			name: "undeclared_field",
			yaml: "schema: flows/v1\nid: bad-field\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.harness\n    outputs:\n      ok: {type: boolean}\n  b:\n    uses: core.command\n    with:\n      x: ${{ nodes.a.outputs.nope }}\n",
			code: "undeclared_field",
		},
		{
			name: "cycle",
			yaml: "schema: flows/v1\nid: cyc\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n    needs: [b]\n  b:\n    uses: core.command\n    needs: [a]\n",
			code: "cycle_detected",
		},
		{
			name: "unreachable_branch",
			yaml: "schema: flows/v1\nid: branch\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n    when: \"false\"\n",
			code: "unreachable_branch",
		},
		{
			name: "escaping_ref",
			yaml: "schema: flows/v1\nid: esc\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.harness\n    outputs:\n      $ref: ../secret.json\n",
			code: "invalid_schema_ref",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, pDiags := workflow.ParseBytes(filepath.Join(dir, "workflow.yaml"), []byte(tc.yaml))
			if len(pDiags) > 0 {
				t.Fatalf("unexpected parse error: %+v", pDiags)
			}
			_, diags := workflow.Compile(def, reg, nil)
			if len(diags) == 0 {
				t.Fatalf("expected diagnostic %s, got none", tc.code)
			}
			found := false
			for _, d := range diags {
				if d.Code == tc.code && d.Line > 0 && d.Column > 0 {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected code %s with line/col, got %+v", tc.code, diags)
			}
		})
	}

	// Nested workflow recursion check.
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("schema: flows/v1\nid: parent\nversion: 1.0.0\nnodes:\n  call:\n    workflow: sub\n"), 0600)
	_ = os.WriteFile(filepath.Join(subDir, "workflow.yaml"), []byte("schema: flows/v1\nid: parent\nversion: 1.0.0\nnodes:\n  leaf:\n    uses: core.command\n"), 0600)

	rep := cli.ValidateDirectory(dir, reg)
	if rep.Valid || len(rep.Diagnostics) == 0 || rep.Diagnostics[0].Code != "workflow_recursion" {
		t.Fatalf("expected workflow_recursion diagnostic, got %+v", rep)
	}
}
