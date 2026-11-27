package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestWorkflowRunTransactionAndInterruption(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	spec := NewWorkflowRun{RequestID: "request", WorkflowID: "external", Digest: "package", SnapshotDigest: "snapshot", Inputs: []byte("{}"), Snapshot: []byte("{}"),
		Steps: []WorkflowStep{{NodeID: "step", StepID: "invoke", Kind: "tool", ToolID: "tool", CacheKey: "build", BinaryDigest: "binary", InvocationID: "call"}}}
	broken := spec
	broken.Edges = []WorkflowEdge{{From: "missing", To: "invoke"}}
	if _, _, err := s.CreateWorkflowRun(ctx, broken); err == nil {
		t.Fatal("invalid edge did not roll back run")
	}
	if _, err := s.WorkflowRunByRequest(ctx, spec.RequestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("run survived rollback: %v", err)
	}
	run, created, err := s.CreateWorkflowRun(ctx, spec)
	if err != nil || !created {
		t.Fatalf("run not created: %v", err)
	}
	duplicate, created, err := s.CreateWorkflowRun(ctx, spec)
	if err != nil || created || duplicate.RunID != run.RunID {
		t.Fatalf("idempotency failed: %+v %v", duplicate, err)
	}
	if got := scalar[string](t, s.db, "SELECT pinned_cache_key FROM workflow_steps WHERE node_id=?", "step"); got != "build" {
		t.Fatalf("pinned build = %q", got)
	}
	if _, err := s.StartAttempt(ctx, "step"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkWorkflowStepInFlight(ctx, "step"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUnfinishedRunsInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range view.Nodes {
		if node.Status != Interrupted || (node.ID == "step" && node.Detail == "") {
			t.Fatalf("wrong interruption state: %+v", node)
		}
	}
	if _, err := s.RemoveSubtree(ctx, run.NodeID); err != nil {
		t.Fatal(err)
	}
	if got := scalar[int](t, s.db, "SELECT count(*) FROM workflow_steps"); got != 0 {
		t.Fatalf("%d workflow steps remain", got)
	}
	if got := scalar[int](t, s.db, "SELECT count(*) FROM workflow_runs"); got != 0 {
		t.Fatalf("%d workflow runs remain", got)
	}
}
