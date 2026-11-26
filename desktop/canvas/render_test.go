package canvas

import (
	"testing"
	"time"
)

func TestRenderMarksSelectionScalesAndIdleBehavior(t *testing.T) {
	allStates := []struct {
		kind  NodeKind
		state NodeState
		shape ShapeKind
		mark  string
	}{
		{KindSession, StatePending, ShapeCircle, "pending_ring"},
		{KindTool, StateRunning, ShapeSquare, "running_arc"},
		{KindWatch, StateWatching, ShapeDiamond, "watching_eye"},
		{KindGroup, StateNeedsAttention, ShapeHexagon, "attention_bang"},
		{KindTool, StateCompleted, ShapeSquare, "completed_check"},
		{KindTool, StateError, ShapeSquare, "error_cross"},
		{KindSession, StateStopped, ShapeCircle, "stopped_square"},
	}

	var nodes []Node
	for i, tc := range allStates {
		nodes = append(nodes, Node{
			ID:        string(rune('a' + i)),
			RunID:     "r1",
			Kind:      tc.kind,
			Title:     "Node " + string(tc.state),
			Detail:    "detail",
			State:     tc.state,
			HasNotice: i == 0,
		})
	}
	edges := []Edge{
		{ID: "e1", RunID: "r1", FromID: "a", ToID: "b", Kind: EdgeDependency},
		{ID: "e2", RunID: "r1", FromID: "a", ToID: "c", Kind: EdgeContainment},
	}

	wl := Layout(Graph{Nodes: nodes, Edges: edges}, DefaultLayoutConfig(), nil)
	selected := map[string]bool{"b": true}

	scene1x := BuildScene(wl, selected, 1.0, 1.2)
	scene2x := BuildScene(wl, selected, 2.0, 1.2)

	if !scene1x.NeedsAnimation {
		t.Fatalf("expected NeedsAnimation=true when running node is present")
	}
	if AnimationInterval != time.Second/30 {
		t.Fatalf("expected 30 fps animation interval, got %v", AnimationInterval)
	}

	// Verify edge endpoints scale cleanly at 1x and 2x.
	if len(scene1x.Edges) != 2 || len(scene2x.Edges) != 2 {
		t.Fatalf("expected 2 edges in both scenes")
	}
	if scene2x.Edges[0].From.X != scene1x.Edges[0].From.X*2 || scene2x.Edges[0].To.Y != scene1x.Edges[0].To.Y*2 {
		t.Fatalf("edge endpoints did not scale 2x: 1x=%+v 2x=%+v", scene1x.Edges[0], scene2x.Edges[0])
	}
	if scene1x.Edges[1].Style != StyleFaintDashed || scene1x.Edges[1].Alpha >= 0.5 {
		t.Fatalf("containment edge should be faint dashed, got %+v", scene1x.Edges[1])
	}

	// Verify every mark, shape, selection ring, and accessibility element.
	byID := make(map[string]RenderNode, len(scene1x.Nodes))
	for _, rn := range scene1x.Nodes {
		byID[rn.ID] = rn
	}
	for i, tc := range allStates {
		id := string(rune('a' + i))
		rn := byID[id]
		if rn.Shape != tc.shape || rn.MarkSymbol != tc.mark {
			t.Fatalf("node %s: got shape=%s mark=%s, want %s/%s", id, rn.Shape, rn.MarkSymbol, tc.shape, tc.mark)
		}
		if rn.Accessibility.State != string(tc.state) || len(rn.Accessibility.Actions) == 0 {
			t.Fatalf("node %s missing accessibility state/actions: %+v", id, rn.Accessibility)
		}
	}
	if !byID["b"].SelectionRing || byID["a"].SelectionRing {
		t.Fatalf("selection ring mismatch")
	}

	// Verify Retina hit-testing at 2x scale.
	nlA := wl.Nodes["a"]
	retinaPt := Point{X: (nlA.Center.X) * 2.0, Y: (nlA.Center.Y) * 2.0}
	if hitID, ok := HitTestNode(wl, retinaPt, Point{}, 1.0, 2.0); !ok || hitID != "a" {
		t.Fatalf("HitTestNode at 2x scale: got (%q, %v), want (a, true)", hitID, ok)
	}

	// Verify idle redraw behavior: no running nodes -> NeedsAnimation=false.
	idleNodes := []Node{{ID: "z", RunID: "r2", Kind: KindTool, Title: "done", State: StateCompleted}}
	idleWL := Layout(Graph{Nodes: idleNodes}, DefaultLayoutConfig(), nil)
	if NeedsAnimation(idleWL) {
		t.Fatalf("expected NeedsAnimation=false when no running marks are visible")
	}
}
