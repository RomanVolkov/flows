package editor_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romanvolkov/flows/desktop/editor"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestRoundTripEditPreservesCommentsExternalConflictInvalidBindingAndPinnedRun(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()

	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.yaml")
	initial := `# Leading workflow comment
schema: flows/v1
id: roundtrip-flow
version: 1.0.0
x-extension: keep-me
nodes:
  step1:
    uses: core.command # inline comment
    with:
      command: /bin/echo
`
	if err := os.WriteFile(wfPath, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	// Pin a LaunchSnapshot of v1 before editing.
	def1, _ := workflow.ParseFile(wfPath)
	cw1, _ := workflow.Compile(def1, reg, nil)
	pinnedSnap, err := workflow.CreateSnapshot(dir, cw1, reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Open -> visual edit -> save -> reopen, preserving comments & x-extension.
	ed, err := editor.OpenWorkflowFile(wfPath, reg)
	if err != nil || len(ed.Diagnostics) > 0 {
		t.Fatalf("OpenWorkflowFile: err=%v diags=%+v", err, ed.Diagnostics)
	}
	if err := ed.AddNode("step2", "core.command"); err != nil {
		t.Fatal(err)
	}
	if err := ed.AddDependency("step2", "step1"); err != nil {
		t.Fatal(err)
	}
	diff, err := ed.PreviewDiff()
	if err != nil || !strings.Contains(diff, "+     uses: core.command") {
		t.Fatalf("unexpected diff:\n%s (err=%v)", diff, err)
	}
	if err := ed.Save(reg, false); err != nil {
		t.Fatalf("Save: %v", err)
	}

	savedBytes, _ := os.ReadFile(wfPath)
	savedStr := string(savedBytes)
	if !strings.Contains(savedStr, "# Leading workflow comment") || !strings.Contains(savedStr, "x-extension: keep-me") || !strings.Contains(savedStr, "step2:") {
		t.Fatalf("round-trip lost comments/extension/step2:\n%s", savedStr)
	}

	// Pinned snapshot of v1 still has only 1 node (step1).
	if len(pinnedSnap.Compiled.Definition.Nodes) != 1 {
		t.Fatalf("pinned snapshot mutated")
	}

	// 2. External edit triggers ErrExternalEdit.
	ed2, err := editor.OpenWorkflowFile(wfPath, reg)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(wfPath, append(savedBytes, []byte("# external edit\n")...), 0600)
	_ = ed2.AddNode("step3", "core.command")
	if err := ed2.Save(reg, false); !errors.Is(err, workflow.ErrExternalEdit) || !ed2.ConflictExternal {
		t.Fatalf("expected ErrExternalEdit, got %v (conflict=%v)", err, ed2.ConflictExternal)
	}

	// 3. Invalid binding is rejected on save.
	ed3, err := editor.OpenWorkflowFile(wfPath, reg)
	if err != nil {
		t.Fatal(err)
	}
	_ = ed3.SetWithBinding("step1", "bad", "${{ nodes.nonexistent.outputs.x }}")
	if err := ed3.Save(reg, false); err == nil {
		t.Fatalf("expected invalid binding to be rejected on save")
	}
}
