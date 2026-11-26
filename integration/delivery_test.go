package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestWatchChangeRecoveryAndFiveGroupDelivery(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "github", Namespace: "github", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{
			{ID: "github.get-pull-request", ExecutionKind: catalog.ExecPlugin, ReadOnly: true},
			{ID: "github.merge-pr", ExecutionKind: catalog.ExecPlugin},
		},
	})
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "gitlab", Namespace: "gitlab", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{
			{ID: "gitlab.get-merge-request", ExecutionKind: catalog.ExecPlugin, ReadOnly: true},
			{ID: "gitlab.merge-mr", ExecutionKind: catalog.ExecPlugin},
		},
	})
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "jira", Namespace: "jira", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "jira.transition-issue", ExecutionKind: catalog.ExecPlugin}},
	})
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "zulip", Namespace: "zulip", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "zulip.resolve-topic", ExecutionKind: catalog.ExecPlugin}},
	})

	def, diags := workflow.ParseFile("../workflows/deliver-change/flows/watch-change.yaml")
	if len(diags) > 0 {
		t.Fatalf("parse watch-change: %+v", diags)
	}
	if _, cdiags := workflow.Compile(def, reg, nil); len(cdiags) > 0 {
		t.Fatalf("compile watch-change: %+v", cdiags)
	}

	// 1. WatchStore restart recovery: cursor, wake time, ordinal, and handled comments.
	watchDir := t.TempDir()
	now := time.Now()
	ws1, err := engine.OpenWatchStore(watchDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws1.StartWatch("watch-mr-12", 2*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	st, err := ws1.RecordPoll("watch-mr-12", 1, engine.ObservationResult{
		Done:   false,
		Cursor: "comment-105",
		Data:   json.RawMessage(`{"handled_comments":["c-101","c-105"]}`),
	}, 30*time.Second, now)
	if err != nil || st.Ordinal != 2 || st.Cursor != "comment-105" {
		t.Fatalf("unexpected watch state: %+v err=%v", st, err)
	}

	// Simulate daemon restart by reopening WatchStore from disk.
	ws2, err := engine.OpenWatchStore(watchDir)
	if err != nil {
		t.Fatal(err)
	}
	dueBefore := ws2.DueWatches(now.Add(10 * time.Second))
	if len(dueBefore) != 0 {
		t.Fatalf("expected 0 due watches before wake time, got %d", len(dueBefore))
	}
	dueAfter := ws2.DueWatches(now.Add(31 * time.Second))
	if len(dueAfter) != 1 || dueAfter[0].Cursor != "comment-105" || dueAfter[0].Ordinal != 2 {
		t.Fatalf("expected recovered watch with cursor=comment-105, got %+v", dueAfter)
	}
	// Obsolete poll ordinal (stale ordinal=1 after already recorded) is rejected.
	if _, err := ws2.RecordPoll("watch-mr-12", 0, engine.ObservationResult{Cursor: "old"}, 30*time.Second, now); err == nil {
		t.Fatal("expected obsolete poll ordinal to be rejected")
	}

	// 2. Five-group multi-repo fixture: shared prerequisite, independent progress, blocked group doesn't freeze others.
	steps, edges := engine.PublishGroups("run-5grp", []engine.GroupDecl{
		{ID: "g1", Name: "RepoA Core"},
		{ID: "g2", Name: "RepoB Client", DependsOn: []engine.DependencyEdge{{FromStepID: "g1", Milestone: "merged"}}},
		{ID: "g3", Name: "RepoA Docs"},
		{ID: "g4", Name: "RepoB CLI (Blocked CI)", DependsOn: []engine.DependencyEdge{{FromStepID: "g2", Milestone: "merged"}}},
		{ID: "g5", Name: "RepoA Telemetry"},
	})
	gm := engine.NewGraphManager()
	gm.InitRun("run-5grp", steps, edges)
	// Independent g3 and g5 can claim and progress even while g4 or g2 waits on g1/CI.
	if err := gm.Claim("run-5grp", "g3.plan"); err != nil {
		t.Fatalf("claim independent g3.plan: %v", err)
	}
	if err := gm.Claim("run-5grp", "g5.plan"); err != nil {
		t.Fatalf("claim independent g5.plan: %v", err)
	}

	// 3. Reconciling an already-opened PR/MR or already-merged state avoids duplicate writes.
	journal, err := engine.OpenEffectJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = journal.RecordIntent(engine.ExternalEffect{
		InvocationID:   "inv-merge-g1",
		AttemptID:      "att-1",
		Action:         "github.merge-pr",
		IdempotencyKey: "repo-a:pr-1:sha-1",
		State:          engine.EffectPending,
	})
	rec, err := journal.Reconcile(context.Background(), "inv-merge-g1", func(_ context.Context, _ string) (bool, string, error) {
		return true, "merged:sha-1", nil
	})
	if err != nil || rec.State != engine.EffectCommitted {
		t.Fatalf("expected committed merge on reconcile, got %+v err=%v", rec, err)
	}
}
