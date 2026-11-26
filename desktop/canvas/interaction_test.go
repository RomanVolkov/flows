package canvas

import (
	"path/filepath"
	"testing"
)

func TestDragParentChildSuccessorInheritanceAndOffsetRestore(t *testing.T) {
	graph := Graph{
		Nodes: []Node{
			{ID: "parent", RunID: "r1", Kind: KindGroup, Title: "Parent", State: StateRunning},
			{ID: "child", RunID: "r1", ParentID: "parent", Kind: KindSession, Title: "Child", State: StateRunning},
			{ID: "sibling", RunID: "r1", ParentID: "parent", Kind: KindTool, Title: "Sibling", State: StatePending},
			{ID: "succ", RunID: "r1", Kind: KindTool, Title: "Successor", State: StatePending},
		},
		Edges: []Edge{
			{ID: "e1", RunID: "r1", FromID: "child", ToID: "succ", Kind: EdgeDependency},
		},
	}

	if ShouldStartDrag(Point{X: 10, Y: 10}, Point{X: 11, Y: 11}) {
		t.Fatalf("drag should not start before 3 points")
	}
	if !ShouldStartDrag(Point{X: 10, Y: 10}, Point{X: 13, Y: 10}) {
		t.Fatalf("drag should start at 3 points")
	}

	wl := Layout(graph, DefaultLayoutConfig(), nil)
	childBefore := wl.Nodes["child"].Bounds
	offsets := make(map[string]Point)

	// Selecting both parent and child must move child ONCE, not twice.
	selected := map[string]bool{"parent": true, "child": true}
	MoveSelected(&wl, graph, selected, Point{X: 50, Y: 25}, offsets)

	childAfter := wl.Nodes["child"].Bounds
	if childAfter.X != childBefore.X+50 || childAfter.Y != childBefore.Y+25 {
		t.Fatalf("child moved by (%.1f, %.1f), want (50, 25)", childAfter.X-childBefore.X, childAfter.Y-childBefore.Y)
	}

	// Successor inherits first causal predecessor's offset.
	inherited, ok := InheritPredecessorOffset("succ", graph, offsets)
	if !ok || inherited.X != 50 || inherited.Y != 25 {
		t.Fatalf("expected inherited offset (50,25), got %+v (ok=%v)", inherited, ok)
	}

	// Persist and restore offsets.
	offPath := filepath.Join(t.TempDir(), "offsets.json")
	if err := SaveOffsets(offPath, offsets); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadOffsets(offPath)
	if err != nil {
		t.Fatal(err)
	}
	if restored["parent"] != (Point{X: 50, Y: 25}) {
		t.Fatalf("restored offsets mismatch: %+v", restored)
	}

	// R resets selected or all.
	ResetOffsets(map[string]bool{"parent": true}, restored)
	if _, exists := restored["parent"]; exists {
		t.Fatalf("expected parent offset reset")
	}
	ResetOffsets(nil, restored)
	if len(restored) != 0 {
		t.Fatalf("expected all offsets cleared, got %+v", restored)
	}
}

func TestBandAtNonUnitZoomNavigationShortcutsAndConfirmation(t *testing.T) {
	graph := Graph{
		Nodes: []Node{
			{ID: "n1", RunID: "r1", Kind: KindSession, Title: "n1", State: StateRunning},
			{ID: "n2", RunID: "r1", Kind: KindTool, Title: "n2", State: StatePending},
			{ID: "n3", RunID: "r2", Kind: KindSession, Title: "n3", State: StatePending},
		},
		Edges: []Edge{
			{ID: "e1", RunID: "r1", FromID: "n1", ToID: "n2", Kind: EdgeDependency},
		},
	}
	wl := Layout(graph, DefaultLayoutConfig(), nil)
	cam := Camera{Pan: Point{X: 100, Y: 50}, Zoom: 0.5}
	view := Size{Width: 1000, Height: 800}

	// Band selection in screen space at 0.5x zoom covering n1.
	n1 := wl.Nodes["n1"]
	tl := cam.WorldToScreen(Point{X: n1.Bounds.X - 10, Y: n1.Bounds.Y - 10})
	br := cam.WorldToScreen(Point{X: n1.Bounds.X + n1.Bounds.Width + 10, Y: n1.Bounds.Y + n1.Bounds.Height + 10})

	sel := NewSelection()
	sel.BandSelect(wl, cam, Rect{X: tl.X, Y: tl.Y, Width: br.X - tl.X, Height: br.Y - tl.Y}, false)
	if !sel.Items["n1"] || len(sel.Items) != 1 {
		t.Fatalf("band select at 0.5x zoom failed: %+v", sel.Items)
	}

	// Directional navigation (down from n1 -> n2).
	next, ok := sel.NavigateDirection(wl, &cam, view, DirDown)
	if !ok || next != "n2" {
		t.Fatalf("NavigateDirection(down) = (%q, %v), want (n2, true)", next, ok)
	}

	// Suppress shortcuts while typing or in modal dialogs.
	if act := sel.HandleShortcut("a", true, true, false, wl, &cam, view, nil); act != ShortcutSuppressed {
		t.Fatalf("expected ShortcutSuppressed while typing, got %s", act)
	}
	if act := sel.HandleShortcut("Escape", false, false, true, wl, &cam, view, nil); act != ShortcutSuppressed {
		t.Fatalf("expected ShortcutSuppressed in modal, got %s", act)
	}

	// Cmd+A selects all, Cmd+Backspace opens confirmation, CancelRemove preserves selection.
	if act := sel.HandleShortcut("a", true, false, false, wl, &cam, view, nil); act != ShortcutSelectAll || len(sel.Items) != 3 {
		t.Fatalf("Cmd+A failed: act=%s items=%+v", act, sel.Items)
	}
	if act := sel.HandleShortcut("Backspace", true, false, false, wl, &cam, view, nil); act != ShortcutRequestRemove || !sel.ConfirmationOpen {
		t.Fatalf("Cmd+Backspace failed: act=%s confirm=%v", act, sel.ConfirmationOpen)
	}
	sel.CancelRemove()
	if sel.ConfirmationOpen || len(sel.Items) != 3 {
		t.Fatalf("CancelRemove should dismiss confirmation and keep selection")
	}
}
