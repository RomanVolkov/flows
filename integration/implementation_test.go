package integration_test

import (
	"context"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestImplementGroupAndCheckpointRecovery(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	for _, spec := range []struct{ pkg, op string }{
		{"git", "git.prepare-checkout"},
		{"git", "git.push-branch"},
		{"github", "github.open-or-reuse-pr"},
		{"jira", "jira.transition-issue"},
		{"zulip", "zulip.open-or-reuse-topic"},
	} {
		_ = reg.RegisterPackage(catalog.PackageProvenance{
			PackageID: spec.pkg + "-" + spec.op, Namespace: spec.pkg, Version: "1.0.0", Origin: "user", Enabled: true,
			Operations: []catalog.OperationDescriptor{{ID: spec.op, ExecutionKind: catalog.ExecPlugin}},
		})
	}
	// Register all git ops under one package, github/jira/zulip under theirs.
	reg2 := catalog.NewRegistry()
	_ = reg2.RegisterBuiltins()
	_ = reg2.RegisterPackage(catalog.PackageProvenance{
		PackageID: "git", Namespace: "git", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{
			{ID: "git.prepare-checkout", ExecutionKind: catalog.ExecPlugin},
			{ID: "git.push-branch", ExecutionKind: catalog.ExecPlugin},
		},
	})
	_ = reg2.RegisterPackage(catalog.PackageProvenance{
		PackageID: "github", Namespace: "github", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "github.open-or-reuse-pr", ExecutionKind: catalog.ExecPlugin}},
	})
	_ = reg2.RegisterPackage(catalog.PackageProvenance{
		PackageID: "jira", Namespace: "jira", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "jira.transition-issue", ExecutionKind: catalog.ExecPlugin}},
	})
	_ = reg2.RegisterPackage(catalog.PackageProvenance{
		PackageID: "zulip", Namespace: "zulip", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "zulip.open-or-reuse-topic", ExecutionKind: catalog.ExecPlugin}},
	})

	def, diags := workflow.ParseFile("../workflows/deliver-change/flows/implement-group.yaml")
	if len(diags) > 0 {
		t.Fatalf("parse implement-group: %+v", diags)
	}
	if _, cdiags := workflow.Compile(def, reg2, nil); len(cdiags) > 0 {
		t.Fatalf("compile implement-group: %+v", cdiags)
	}

	// Independent vs waiting groups: g2 waits until g1 publishes "merged" milestone.
	steps, edges := engine.PublishGroups("run-impl-1", []engine.GroupDecl{
		{ID: "g1", Name: "Foundation"},
		{ID: "g2", Name: "Dependent", DependsOn: []engine.DependencyEdge{{FromStepID: "g1", Milestone: "merged"}}},
	})
	gm := engine.NewGraphManager()
	gm.InitRun("run-impl-1", steps, edges)
	// Before g1 merges, g2.implement is still waiting while g1.implement can proceed once g1.plan completes.
	snap, ok := gm.RunSnapshot("run-impl-1")
	if !ok || snap.Steps["g2.implement"].Status != engine.StepWaiting {
		t.Fatalf("expected g2.implement waiting, got %+v", snap.Steps["g2.implement"])
	}

	// Failed verification blocks push/PR, while resume after push + daemon crash reconciles via EffectJournal.
	journal, err := engine.OpenEffectJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eff, err := journal.RecordIntent(engine.ExternalEffect{
		InvocationID:   "inv-open-pr-g1",
		AttemptID:      "att-1",
		Action:         "github.open-or-reuse-pr",
		IdempotencyKey: "repo-a:feat/g1",
		State:          engine.EffectPending,
	})
	if err != nil || eff.State != engine.EffectPending {
		t.Fatalf("record intent: %+v err=%v", eff, err)
	}
	// Simulate provider success followed by daemon crash before checkpoint commit:
	// On restart, Reconcile queries remote provider and commits without re-creating PR.
	remoteCalls := 0
	reconciled, err := journal.Reconcile(context.Background(), "inv-open-pr-g1", func(_ context.Context, key string) (bool, string, error) {
		remoteCalls++
		return key == "repo-a:feat/g1", "https://gh/flows/core/pull/99", nil
	})
	if err != nil || reconciled.State != engine.EffectCommitted || reconciled.RemoteRef != "https://gh/flows/core/pull/99" || remoteCalls != 1 {
		t.Fatalf("reconcile failed: %+v err=%v calls=%d", reconciled, err, remoteCalls)
	}
}
