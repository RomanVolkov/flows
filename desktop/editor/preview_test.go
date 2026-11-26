package editor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/desktop/canvas"
	"github.com/romanvolkov/flows/desktop/editor"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestTemplatePreviewSingleNodeVsLiveFanOutNestedAndViewport(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "github", Namespace: "github", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "github.list-pull-requests", Description: "list", ExecutionKind: catalog.ExecPlugin, ReadOnly: true}},
	})

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "schemas"), 0700)
	_ = os.WriteFile(filepath.Join(dir, "schemas", "out.json"), []byte(`{"type":"object","properties":{"summary":{"type":"string"}}}`), 0600)
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0700)
	_ = os.WriteFile(filepath.Join(dir, "sub", "workflow.yaml"), []byte("schema: flows/v1\nid: subflow\nversion: 1.0.0\nnodes:\n  leaf:\n    uses: core.command\n"), 0600)

	wfYAML := `schema: flows/v1
id: review-open-prs
version: 1.0.0
inputs:
  repository: {type: string, required: true}
nodes:
  discover:
    uses: github.list-pull-requests
  review:
    uses: core.harness
    for_each:
      items: ${{ nodes.discover.outputs.pull_requests }}
      key: ${{ item.id }}
    outputs:
      approved: {type: boolean, required: true}
      findings: {type: array, items: {type: string}}
  summarize:
    uses: core.harness
    needs: [review]
    outputs:
      $ref: ./schemas/out.json
  nested:
    workflow: sub
    needs: [summarize]
`
	def, pDiags := workflow.ParseBytes(filepath.Join(dir, "workflow.yaml"), []byte(wfYAML))
	if len(pDiags) > 0 {
		t.Fatal(pDiags)
	}
	loader := func(ref string) (*workflow.Definition, error) {
		sub, d := workflow.ParseFile(filepath.Join(dir, ref, "workflow.yaml"))
		if len(d) > 0 {
			return nil, d[0]
		}
		return sub, nil
	}
	cw, cDiags := workflow.Compile(def, reg, loader)
	if len(cDiags) > 0 {
		t.Fatal(cDiags)
	}

	// 1. Template preview has 1 review node with 0...n edge (not 3 materialized live reviews).
	prev := editor.BuildTemplatePreview(cw, loader)
	if len(prev.Nodes) != 4 {
		t.Fatalf("expected 4 declared template nodes, got %d", len(prev.Nodes))
	}
	var hasCollectionEdge bool
	for _, e := range prev.Edges {
		if e.ToID == "review" && e.Kind == editor.EdgeCollection && e.Label == "0…n" {
			hasCollectionEdge = true
		}
	}
	if !hasCollectionEdge {
		t.Fatalf("expected 0…n collection edge to review, got %+v", prev.Edges)
	}
	if len(prev.NestedBoundaries) != 1 || prev.NestedBoundaries[0].ChildCount != 1 {
		t.Fatalf("expected nested boundary with 1 child, got %+v", prev.NestedBoundaries)
	}
	// File-backed $ref port resolved on summarize.
	if prev.Nodes[2].OutputPorts[0] != "summary" {
		t.Fatalf("expected resolved $ref port 'summary', got %+v", prev.Nodes[2].OutputPorts)
	}

	// 2. Execution viewport preserved when opening template pinned to an older run and returning.
	sess := editor.NewEditorSession()
	sess.ExecutionCamera.PanBy(250, -80)
	sess.OpenTemplate(editor.TemplateDocument{
		DefinitionID:  "review-open-prs",
		Version:       "1.0.0",
		PinnedToRunID: "run-old-42",
		Compiled:      cw,
	})
	if sess.Mode != editor.ModeTemplate || sess.ActiveTemplate.PinnedToRunID != "run-old-42" {
		t.Fatalf("expected template mode pinned to run-old-42")
	}
	tCam := sess.ActiveTemplateCamera()
	tCam.ZoomAt(canvas.Point{X: 100, Y: 100}, 1.5)
	sess.SaveTemplateCamera(*tCam)

	restored := sess.ReturnToExecution()
	if sess.Mode != editor.ModeExecution || restored.Pan.X != 250 || restored.Zoom != 1.0 {
		t.Fatalf("execution camera not preserved: %+v", restored)
	}
}
