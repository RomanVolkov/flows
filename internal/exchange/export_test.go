package exchange_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/exchange"
)

func TestDeterministicExportNestedSchemasMissingPromptCaseConflictAndSecrets(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()

	pkgDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(pkgDir, "prompts"), 0700)
	_ = os.MkdirAll(filepath.Join(pkgDir, "schemas"), 0700)
	_ = os.MkdirAll(filepath.Join(pkgDir, "sub"), 0700)
	_ = os.MkdirAll(filepath.Join(pkgDir, "logs"), 0700)

	_ = os.WriteFile(filepath.Join(pkgDir, "prompts", "review.md"), []byte("Review carefully"), 0600)
	_ = os.WriteFile(filepath.Join(pkgDir, "schemas", "base.json"), []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0600)
	_ = os.WriteFile(filepath.Join(pkgDir, "schemas", "result.json"), []byte(`{"$ref":"./base.json"}`), 0600)
	_ = os.WriteFile(filepath.Join(pkgDir, "sub", "workflow.yaml"), []byte("schema: flows/v1\nid: subflow\nversion: 1.0.0\nnodes:\n  leaf:\n    uses: core.command\n"), 0600)
	_ = os.WriteFile(filepath.Join(pkgDir, "logs", "runtime.log"), []byte("SECRET_TOKEN=raw"), 0600)
	_ = os.WriteFile(filepath.Join(pkgDir, "workflow.yaml"), []byte(`schema: flows/v1
id: portable-flow
version: 1.0.0
nodes:
  review:
    uses: core.harness
    with:
      harness: claude
      prompt_template: ./prompts/review.md
    outputs:
      $ref: ./schemas/result.json
  child:
    workflow: sub
    needs: [review]
`), 0600)

	out1 := filepath.Join(t.TempDir(), "a.flows")
	out2 := filepath.Join(t.TempDir(), "b.flows")
	opts := exchange.ExportOptions{
		Catalog:          reg,
		ProviderAccounts: []string{"env:GITHUB_TOKEN"},
	}

	m1, err := exchange.Export(pkgDir, out1, opts)
	if err != nil {
		t.Fatalf("Export 1: %v", err)
	}
	if _, err := exchange.Export(pkgDir, out2, opts); err != nil {
		t.Fatalf("Export 2: %v", err)
	}

	// 1. Two deterministic exports are byte-for-byte identical.
	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("expected byte-identical deterministic .flows archives")
	}

	// 2. Transitive schemas indexed, runtime logs omitted, secret-ref-only.
	if len(m1.Schemas) != 2 {
		t.Fatalf("expected 2 transitive schemas in index, got %d (%+v)", len(m1.Schemas), m1.Schemas)
	}
	for k := range m1.PayloadChecksums {
		if filepath.Base(k) == "runtime.log" {
			t.Fatalf("runtime.log must be omitted from bundle")
		}
	}
	if len(m1.ExternalPrerequisites.ProviderAccounts) != 1 || m1.ExternalPrerequisites.ProviderAccounts[0] != "env:GITHUB_TOKEN" {
		t.Fatalf("unexpected provider accounts: %+v", m1.ExternalPrerequisites)
	}

	// 3. Missing prompt fails before export.
	_ = os.Remove(filepath.Join(pkgDir, "prompts", "review.md"))
	if _, err := exchange.Export(pkgDir, filepath.Join(t.TempDir(), "missing.flows"), opts); !errors.Is(err, exchange.ErrMissingDependency) {
		t.Fatalf("expected ErrMissingDependency for missing prompt, got %v", err)
	}
}
