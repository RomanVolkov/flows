package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/plugin"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestImplementGroupAndCheckpointRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Populate catalog from real plugin binaries via plugin.Describe.
	reg := catalog.NewRegistry()
	if err := reg.RegisterBuiltins(); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct{ path, name string }{
		{"./plugins/git/cmd/plugin", "git-plugin"},
		{"./plugins/github/cmd/plugin", "github-plugin"},
		{"./plugins/jira/cmd/plugin", "jira-plugin"},
		{"./plugins/zulip/cmd/plugin", "zulip-plugin"},
	} {
		bin := buildPluginBin(t, spec.path, spec.name)
		m, err := plugin.Describe(ctx, bin)
		if err != nil {
			t.Fatalf("describe %s: %v", spec.name, err)
		}
		var ops []catalog.OperationDescriptor
		for _, op := range m.Operations {
			ops = append(ops, catalog.OperationDescriptor{
				ID: op.ID, Description: op.Description, ExecutionKind: catalog.ExecPlugin, ReadOnly: op.ReadOnly,
			})
		}
		if err := reg.RegisterPackage(catalog.PackageProvenance{
			PackageID: m.ID, Namespace: m.ID, Version: m.Version, Owner: m.Owner, Origin: "user", Enabled: true, Operations: ops,
		}); err != nil {
			t.Fatalf("register %s: %v", m.ID, err)
		}
	}

	def, diags := workflow.ParseFile("../workflows/deliver-change/flows/implement-group.yaml")
	if len(diags) > 0 {
		t.Fatalf("parse implement-group: %+v", diags)
	}
	if _, cdiags := workflow.Compile(def, reg, nil); len(cdiags) > 0 {
		t.Fatalf("compile implement-group: %+v", cdiags)
	}

	// Independent vs waiting groups: g2 waits until g1 publishes "merged" milestone.
	steps, edges := engine.PublishGroups("run-impl-1", []engine.GroupDecl{
		{ID: "g1", Name: "Foundation"},
		{ID: "g2", Name: "Dependent", DependsOn: []engine.DependencyEdge{{FromStepID: "g1", Milestone: "merged"}}},
	})
	gm := engine.NewGraphManager()
	gm.InitRun("run-impl-1", steps, edges)
	snap, ok := gm.RunSnapshot("run-impl-1")
	if !ok || snap.Steps["g2.implement"].Status != engine.StepWaiting {
		t.Fatalf("expected g2.implement waiting, got %+v", snap.Steps["g2.implement"])
	}

	// Simulate provider success followed by daemon crash before checkpoint commit:
	// On restart, Reconcile queries remote provider and commits without re-creating PR.
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
	remoteCalls := 0
	reconciled, err := journal.Reconcile(ctx, "inv-open-pr-g1", func(_ context.Context, key string) (bool, string, error) {
		remoteCalls++
		return key == "repo-a:feat/g1", "https://gh/flows/core/pull/99", nil
	})
	if err != nil || reconciled.State != engine.EffectCommitted || reconciled.RemoteRef != "https://gh/flows/core/pull/99" || remoteCalls != 1 {
		t.Fatalf("reconcile failed: %+v err=%v calls=%d", reconciled, err, remoteCalls)
	}
}
