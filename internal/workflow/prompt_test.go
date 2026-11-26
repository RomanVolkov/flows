package workflow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestPromptRenderingBindingsSnapshotAndEditsDuringRun(t *testing.T) {
	reg := testCatalog(t)
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0700); err != nil {
		t.Fatal(err)
	}
	tplPath := filepath.Join(dir, "prompts", "review.md")
	if err := os.WriteFile(tplPath, []byte("Review PR {{ pr_id }} in {{ repo }} with token {{ token }}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(reviewOpenPRsYAML), 0600); err != nil {
		t.Fatal(err)
	}

	def, pDiags := workflow.ParseFile(filepath.Join(dir, "workflow.yaml"))
	if len(pDiags) > 0 {
		t.Fatal(pDiags)
	}
	cw, cDiags := workflow.Compile(def, reg, nil)
	if len(cDiags) > 0 {
		t.Fatal(cDiags)
	}

	// Snapshot at launch.
	snap, err := workflow.CreateSnapshot(dir, cw, reg, nil)
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.OperationVersions["github.list-pull-requests"] != "1.0.0" {
		t.Fatalf("expected pinned op version 1.0.0, got %+v", snap.OperationVersions)
	}

	// Edit package on disk during the run; snapshot must retain original template!
	if err := os.WriteFile(tplPath, []byte("MUTATED TEMPLATE"), 0600); err != nil {
		t.Fatal(err)
	}
	pinnedTpl := snap.Templates["prompts/review.md"]
	if !strings.Contains(pinnedTpl, "Review PR") {
		t.Fatalf("expected pinned template, got %q", pinnedTpl)
	}

	// Render prompt deterministically with credential redaction.
	rp, err := workflow.RenderPrompt("prompts/review.md", pinnedTpl, map[string]any{
		"pr_id": "42",
		"repo":  "flows/core",
		"token": "ghp_supersecret123",
	}, []string{"ghp_supersecret123"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rp.RedactedText, "ghp_supersecret123") || !strings.Contains(rp.RedactedText, "[REDACTED]") {
		t.Fatalf("expected secret redacted in %q", rp.RedactedText)
	}

	// Test zero and multiple PRs binding resolution.
	inputs, err := workflow.ResolveInputs(def, map[string]any{"repository": "flows/core"})
	if err != nil {
		t.Fatal(err)
	}
	ctxZero := workflow.BindContext{
		Inputs: inputs,
		NodeOutputs: map[string]map[string]any{
			"discover": {"pull_requests": []any{}},
		},
	}
	valZero, err := workflow.EvalExpr("nodes.discover.outputs.pull_requests", ctxZero)
	if err != nil || len(valZero.([]any)) != 0 {
		t.Fatalf("zero PRs binding failed: val=%v err=%v", valZero, err)
	}

	ctxMany := workflow.BindContext{
		Inputs: inputs,
		Item:   map[string]any{"id": "pr-7", "title": "Fix bug"},
	}
	itemID, err := workflow.EvalExpr("item.id", ctxMany)
	if err != nil || itemID != "pr-7" {
		t.Fatalf("multiple PRs item binding failed: %v %v", itemID, err)
	}

	// Invalid nested binding must fail.
	if _, err := workflow.EvalExpr("item.id.nested", ctxMany); err == nil {
		t.Fatalf("expected error for invalid nested binding")
	}

	// Missing template must fail snapshot.
	_ = os.Remove(tplPath)
	if _, err := workflow.CreateSnapshot(dir, cw, reg, nil); err == nil {
		t.Fatalf("expected error for missing template")
	}

	// Disabled/incompatible plugin version must fail snapshot.
	_ = os.WriteFile(tplPath, []byte("ok"), 0600)
	_ = reg.SetPackageEnabled("github", false)
	if _, err := workflow.CreateSnapshot(dir, cw, reg, nil); err == nil {
		t.Fatalf("expected error for disabled plugin")
	}

	// Pinning newly discovered operation during live edit.
	if err := reg.RegisterPackage(catalog.PackageProvenance{
		PackageID:  "extra",
		Namespace:  "extra",
		Version:    "2.1.0",
		Origin:     "user",
		Enabled:    true,
		Operations: []catalog.OperationDescriptor{{ID: "extra.check", Description: "check", ExecutionKind: catalog.ExecPlugin}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := snap.PinAddition("extra.check", reg); err != nil || snap.OperationVersions["extra.check"] != "2.1.0" {
		t.Fatalf("PinAddition failed: err=%v versions=%+v", err, snap.OperationVersions)
	}
}
