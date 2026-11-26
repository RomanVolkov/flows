package engine_test

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/host"
)

func TestGraphEditsClaimRaceRemovalConflictCycleAndCancel(t *testing.T) {
	gm := engine.NewGraphManager()
	gm.InitRun("run-g", []engine.LogicalStep{
		{ID: "a", RunID: "run-g", Kind: "session", Name: "a", Status: engine.StepReady},
		{ID: "b", RunID: "run-g", Kind: "tool", Name: "b", Status: engine.StepWaiting},
		{ID: "c", RunID: "run-g", Kind: "tool", Name: "c", Status: engine.StepWaiting},
	}, []engine.DependencyEdge{
		{FromStepID: "a", ToStepID: "b"},
	})

	// 1. Claim step a; late edge targeting claimed step a must be rejected.
	if err := gm.Claim("run-g", "a"); err != nil {
		t.Fatalf("Claim(a): %v", err)
	}
	_, err := gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-g",
		ExpectedRevision: 1,
		IdempotencyKey:   "k-late",
		Operations: []engine.EditOperation{
			{Op: engine.OpAddEdge, Edge: &engine.DependencyEdge{FromStepID: "b", ToStepID: "a"}},
		},
	})
	var conflict *engine.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected EditConflictError for late edge onto claimed node, got %v", err)
	}

	// 2. Remove pending node c -> retained in RemovedHistory.
	res, err := gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-g",
		ExpectedRevision: 1,
		IdempotencyKey:   "k-rem-c",
		Operations: []engine.EditOperation{
			{Op: engine.OpRemoveNode, NodeID: "c", PackageID: "pkg1", PackageRevision: "sha256:111"},
		},
	})
	if err != nil || res.NewRevision != 2 || len(res.RemovedPending) != 1 {
		t.Fatalf("remove pending failed: res=%+v err=%v", res, err)
	}
	// Idempotency key returns identical result.
	resDup, err := gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-g",
		ExpectedRevision: 1,
		IdempotencyKey:   "k-rem-c",
	})
	if err != nil || resDup.NewRevision != 2 {
		t.Fatalf("idempotent edit replay failed: res=%+v err=%v", resDup, err)
	}

	// 3. Conflicting expected revision returns current revision.
	_, err = gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-g",
		ExpectedRevision: 1,
		IdempotencyKey:   "k-stale",
		Operations: []engine.EditOperation{
			{Op: engine.OpAddNode, Node: &engine.LogicalStep{ID: "d", Kind: "tool", Name: "d"}},
		},
	})
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 {
		t.Fatalf("expected conflict with CurrentRevision=2, got %v", err)
	}

	// 4. Cycle rejection rolls back batch.
	_, err = gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-g",
		ExpectedRevision: 2,
		IdempotencyKey:   "k-cyc",
		Operations: []engine.EditOperation{
			{Op: engine.OpAddNode, Node: &engine.LogicalStep{ID: "x", Kind: "tool", Name: "x"}},
			{Op: engine.OpAddNode, Node: &engine.LogicalStep{ID: "y", Kind: "tool", Name: "y"}},
			{Op: engine.OpAddEdge, Edge: &engine.DependencyEdge{FromStepID: "x", ToStepID: "y"}},
			{Op: engine.OpAddEdge, Edge: &engine.DependencyEdge{FromStepID: "y", ToStepID: "x"}},
		},
	})
	if err == nil {
		t.Fatalf("expected cycle rejection")
	}

	// 5. Repeat cancellation + checkout lease retained when writer is still alive.
	cmd := exec.Command("/bin/sleep", "2")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	proc, err := host.ProbeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	ca1, err := gm.CancelStep("run-g", "a", "att-1", "harness", "repo-1", &proc, true)
	if err != nil || ca1.LeaseStatus != host.LeaseUnresolved || !ca1.CheckoutRetained {
		t.Fatalf("unexpected CancelStep: %+v err=%v", ca1, err)
	}
	ca2, err := gm.CancelStep("run-g", "a", "att-1", "harness", "repo-1", &proc, true)
	if err != nil || ca2.AttemptID != "att-1" {
		t.Fatalf("repeat CancelStep failed: %+v err=%v", ca2, err)
	}
}
