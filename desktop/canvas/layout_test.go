package canvas

import (
	"fmt"
	"testing"
)

func TestLayoutDeepChainDiamondFiveGroupsSingletonAndTwoRoots(t *testing.T) {
	cfg := DefaultLayoutConfig()

	// 1. Deep chain: c0 -> c1 -> c2 -> c3
	// 2. Diamond join: d0 -> {d1, d2} -> d3
	// 3. Five blocked/ready groups: g0..g4 (g0 -> g1 -> g2, g3, g4)
	// 4. Disconnected singleton: s0
	// 5. Two roots in separate run: r1, r2
	nodes := []Node{
		{ID: "c0", RunID: "run-chain", Kind: KindSession, Title: "c0", State: StateCompleted},
		{ID: "c1", RunID: "run-chain", Kind: KindTool, Title: "c1", State: StateCompleted},
		{ID: "c2", RunID: "run-chain", Kind: KindTool, Title: "c2", State: StateRunning},
		{ID: "c3", RunID: "run-chain", Kind: KindWatch, Title: "c3", State: StatePending},

		{ID: "d0", RunID: "run-diamond", Kind: KindSession, Title: "d0", State: StateCompleted},
		{ID: "d1", RunID: "run-diamond", Kind: KindTool, Title: "d1", State: StateCompleted},
		{ID: "d2", RunID: "run-diamond", Kind: KindTool, Title: "d2", State: StateCompleted},
		{ID: "d3", RunID: "run-diamond", Kind: KindSession, Title: "d3", State: StateRunning},

		{ID: "s0", RunID: "run-singleton", Kind: KindSession, Title: "s0", State: StateRunning},
		{ID: "r1", RunID: "run-two", Kind: KindSession, Title: "r1", State: StateRunning},
		{ID: "r2", RunID: "run-two", Kind: KindSession, Title: "r2", State: StatePending},
	}
	for i := range 5 {
		nodes = append(nodes, Node{
			ID:    fmt.Sprintf("g%d", i),
			RunID: "run-groups",
			Kind:  KindGroup,
			Title: fmt.Sprintf("Group %d", i),
			State: StatePending,
		})
	}

	edges := []Edge{
		{ID: "e1", RunID: "run-chain", FromID: "c0", ToID: "c1", Kind: EdgeDependency},
		{ID: "e2", RunID: "run-chain", FromID: "c1", ToID: "c2", Kind: EdgeDependency},
		{ID: "e3", RunID: "run-chain", FromID: "c2", ToID: "c3", Kind: EdgeDependency},

		{ID: "ed1", RunID: "run-diamond", FromID: "d0", ToID: "d1", Kind: EdgeDependency},
		{ID: "ed2", RunID: "run-diamond", FromID: "d0", ToID: "d2", Kind: EdgeDependency},
		{ID: "ed3", RunID: "run-diamond", FromID: "d1", ToID: "d3", Kind: EdgeDependency},
		{ID: "ed4", RunID: "run-diamond", FromID: "d2", ToID: "d3", Kind: EdgeDependency},

		{ID: "eg1", RunID: "run-groups", FromID: "g0", ToID: "g1", Kind: EdgeDependency, Milestone: "merged"},
		{ID: "eg2", RunID: "run-groups", FromID: "g1", ToID: "g2", Kind: EdgeDependency, Milestone: "merged"},
	}

	wl := Layout(Graph{Nodes: nodes, Edges: edges}, cfg, nil)
	if len(wl.Nodes) != len(nodes) {
		t.Fatalf("laid out %d nodes, want %d", len(wl.Nodes), len(nodes))
	}

	// Verify arrows go down from prerequisites and labels remain horizontal.
	for _, el := range wl.Edges {
		if el.To.Y <= el.From.Y {
			t.Fatalf("edge %s->%s does not go downward: from.Y=%.1f to.Y=%.1f", el.Edge.FromID, el.Edge.ToID, el.From.Y, el.To.Y)
		}
		if !el.HorizontalLabel {
			t.Fatalf("edge %s label must be horizontal", el.Edge.ID)
		}
	}

	// Verify ranks increase down the deep chain and diamond join.
	if !(wl.Nodes["c0"].Rank < wl.Nodes["c1"].Rank && wl.Nodes["c1"].Rank < wl.Nodes["c2"].Rank && wl.Nodes["c2"].Rank < wl.Nodes["c3"].Rank) {
		t.Fatalf("deep chain ranks not strictly increasing")
	}
	if wl.Nodes["d3"].Rank != 2 {
		t.Fatalf("diamond join d3 rank = %d, want 2", wl.Nodes["d3"].Rank)
	}

	// Verify component bounds contain every node and label in that component.
	for _, cb := range wl.Components {
		for _, nid := range cb.NodeIDs {
			nl := wl.Nodes[nid]
			if !cb.Bounds.Contains(Point{X: nl.Bounds.X, Y: nl.Bounds.Y}) ||
				!cb.Bounds.Contains(Point{X: nl.Bounds.X + nl.Bounds.Width, Y: nl.LabelBounds.Y + nl.LabelBounds.Height}) {
				t.Fatalf("component %d bounds %+v do not contain node %s (%+v, label %+v)", cb.ID, cb.Bounds, nid, nl.Bounds, nl.LabelBounds)
			}
		}
	}

	// Verify saved positions are preserved on incremental update.
	saved := map[string]Point{
		"s0": {X: 1234, Y: 567},
	}
	wl2 := Layout(Graph{Nodes: nodes, Edges: edges}, cfg, saved)
	if wl2.Nodes["s0"].Bounds.X != 1234 || wl2.Nodes["s0"].Bounds.Y != 567 {
		t.Fatalf("expected saved position preserved, got %+v", wl2.Nodes["s0"].Bounds)
	}
}
