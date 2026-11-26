package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/engine"
)

func TestDispatcherCancelVsStartDuplicateCallbacksMilestonesAndEffects(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()

	gm := engine.NewGraphManager()
	ms := engine.NewMilestoneRegistry()
	ej, err := engine.OpenEffectJournal(filepath.Join(stateDir, "effects"))
	if err != nil {
		t.Fatal(err)
	}

	steps, edges := engine.PublishGroups("run-disp", []engine.GroupDecl{
		{ID: "g0", Name: "G0"},
		{ID: "g1", Name: "G1", DependsOn: []engine.DependencyEdge{{FromStepID: "g0", Milestone: "merged"}}},
	})
	gm.InitRun("run-disp", steps, edges)
	disp := engine.NewDispatcher(stateDir, gm, ms, ej, engine.DispatcherConfig{
		MaxConcurrency: 2,
		MaxIterations:  3,
		MaxRetries:     2,
		MaxRuntime:     5 * time.Minute,
	})

	// 1. Cancel-versus-start: cancel g0.plan before claim -> claim must fail.
	if _, err := gm.CancelStep("run-disp", "g0.plan", "att-cancel", "user", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := disp.ClaimAndRecordIntent("run-disp", "g0.plan", "att-late", "ws-1"); err == nil {
		t.Fatalf("cancelled step must not be claimable")
	}

	// 2. Claim g1.plan, record intent, test duplicate callback deduplication & delayed milestone.
	intent, err := disp.ClaimAndRecordIntent("run-disp", "g1.plan", "att-g1p", "ws-1")
	if err != nil {
		t.Fatalf("ClaimAndRecordIntent: %v", err)
	}
	if intent.InvocationID != "inv-att-g1p" {
		t.Fatalf("unexpected invocation id: %+v", intent)
	}

	// First callback accepted; g1.implement still waits on delayed g0@merged milestone.
	accepted, ready := disp.CompleteCallback("run-disp", "g1.plan", "att-g1p", "evt-1", true, map[string]any{"plan": "ok"})
	if !accepted || len(ready) != 0 {
		t.Fatalf("expected accepted=true, ready=0 (waiting on milestone), got accepted=%v ready=%v", accepted, ready)
	}
	// Duplicate callback is rejected/ignored.
	acceptedDup, _ := disp.CompleteCallback("run-disp", "g1.plan", "att-g1p", "evt-1", true, map[string]any{"plan": "ok"})
	if acceptedDup {
		t.Fatalf("duplicate callback must be ignored")
	}

	// 3. Limits enforcement.
	if exceeded, reason := disp.EnforceLimits(4, 0, time.Second); !exceeded || reason == "" {
		t.Fatalf("expected iteration limit exceeded")
	}

	// 4. Restart recovery: pending intents & external effect reconciliation with same InvocationID.
	pendingIntents, err := disp.LoadPendingIntents()
	if err != nil || len(pendingIntents) != 1 || pendingIntents[0].AttemptID != "att-g1p" {
		t.Fatalf("LoadPendingIntents: %+v err=%v", pendingIntents, err)
	}

	eff, err := ej.RecordIntent(engine.ExternalEffect{
		InvocationID:   "inv-pr-1",
		AttemptID:      "att-g1p",
		Action:         "github.create_pr",
		IdempotencyKey: "run-disp:g1:pr",
	})
	if err != nil || eff.State != engine.EffectPending {
		t.Fatalf("RecordIntent: %+v err=%v", eff, err)
	}

	// Ambiguous remote lookup -> EffectNeedsAttention before any retry.
	uncertain, err := ej.Reconcile(ctx, "inv-pr-1", func(context.Context, string) (bool, string, error) {
		return false, "", errors.New("network timeout")
	})
	if err != nil || uncertain.State != engine.EffectNeedsAttention {
		t.Fatalf("expected EffectNeedsAttention on uncertain lookup, got %+v err=%v", uncertain, err)
	}

	// Confirmed remote lookup -> EffectCommitted with RemoteRef.
	confirmed, err := ej.Reconcile(ctx, "inv-pr-1", func(context.Context, string) (bool, string, error) {
		return true, "pr#42", nil
	})
	if err != nil || confirmed.State != engine.EffectCommitted || confirmed.RemoteRef != "pr#42" {
		t.Fatalf("expected EffectCommitted, got %+v err=%v", confirmed, err)
	}
}
