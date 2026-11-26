package integration_test

import (
	"testing"

	"github.com/romanvolkov/flows/desktop/canvas"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/improve"
)

func TestWorkspaceReleaseAcceptance(t *testing.T) {
	// 1. Two roots on one canvas remain independently laid out and selectable.
	g := canvas.Graph{
		Nodes: []canvas.Node{
			{ID: "root-a", Kind: canvas.KindGroup, Title: "Review Open PRs", State: canvas.StateRunning},
			{ID: "root-a.child", ParentID: "root-a", Kind: canvas.KindSession, Title: "Review PR #1", State: canvas.StateRunning},
			{ID: "root-b", Kind: canvas.KindGroup, Title: "Deliver Change", State: canvas.StateRunning},
		},
		Edges: []canvas.Edge{{FromID: "root-a", ToID: "root-a.child", Kind: canvas.EdgeContainment}},
	}
	layout := canvas.Layout(g, canvas.DefaultLayoutConfig(), nil)
	if len(layout.Nodes) != 3 {
		t.Fatalf("expected 3 laid out nodes for two roots, got %d", len(layout.Nodes))
	}
	if _, ok := layout.Nodes["root-a"]; !ok {
		t.Fatal("missing root-a in canvas layout")
	}
	if _, ok := layout.Nodes["root-b"]; !ok {
		t.Fatal("missing root-b in canvas layout")
	}

	// 2. Five-group workflow materialization + cancellation of a live attempt keeps all nodes inspectable.
	steps, gEdges := engine.PublishGroups("run-accept", []engine.GroupDecl{
		{ID: "g1", Name: "G1"},
		{ID: "g2", Name: "G2", DependsOn: []engine.DependencyEdge{{FromStepID: "g1", Milestone: "merged"}}},
		{ID: "g3", Name: "G3"},
		{ID: "g4", Name: "G4"},
		{ID: "g5", Name: "G5"},
	})
	gm := engine.NewGraphManager()
	gm.InitRun("run-accept", steps, gEdges)
	if err := gm.Claim("run-accept", "g1.plan"); err != nil {
		t.Fatal(err)
	}
	cancelled, err := gm.CancelStep("run-accept", "g1.plan", "att-1", "user", "wt-g1", &host.ProcessIdentity{PID: 1234, PGID: 1234, BirthNano: 100}, true)
	if err != nil || cancelled.AttemptID != "att-1" {
		t.Fatalf("cancel live attempt: %+v err=%v", cancelled, err)
	}
	snap, ok := gm.RunSnapshot("run-accept")
	if !ok || len(snap.Steps) != 15 || snap.Steps["g1.plan"].Status != engine.StepCancelled {
		t.Fatalf("expected all 15 steps inspectable with g1.plan cancelled, got %+v", snap.Steps["g1.plan"])
	}

	// 3. Evidence-backed workflow edit validates against installed catalog.
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	prop := improve.Proposal{
		RunID:          "run-accept",
		BaseRevisionID: "rev-1",
		Changes: []improve.ProposedChange{{
			ID:            "chg-1",
			Kind:          improve.ChangeAddCatalogStep,
			TargetNodeID:  "verify",
			OperationID:   "core.command",
			Summary:       "Add lint step before push",
			EvidenceLinks: []string{"ev-1"},
			Accepted:      true,
		}},
	}
	if err := improve.ValidateProposal(prop, reg); err != nil {
		t.Fatalf("validate proposal: %v", err)
	}
}
