package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/schedule"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestReviewOpenPRsWorkflowAndSchedule(t *testing.T) {
	def, diags := workflow.ParseFile("../workflows/review-open-prs/workflow.yaml")
	if len(diags) > 0 {
		t.Fatalf("parse workflow: %+v", diags)
	}
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "github", Namespace: "github", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "github.list-pull-requests", ExecutionKind: catalog.ExecPlugin, ReadOnly: true}},
	})
	_ = reg.RegisterPackage(catalog.PackageProvenance{
		PackageID: "gitlab", Namespace: "gitlab", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "gitlab.list-merge-requests", ExecutionKind: catalog.ExecPlugin, ReadOnly: true}},
	})
	if _, diags := workflow.Compile(def, reg, nil); len(diags) > 0 {
		t.Fatalf("compile diagnostics: %+v", diags)
	}

	// Required unbound input fails cleanly.
	if _, err := workflow.ResolveInputs(def, map[string]any{}); err == nil {
		t.Fatal("expected error for missing required repository input")
	}
	bound, err := workflow.ResolveInputs(def, map[string]any{"repository": "flows/core"})
	if err != nil {
		t.Fatalf("resolve inputs: %v", err)
	}
	if bound["provider"] != "github" || bound["reviewer_harness"] != "claude" {
		t.Fatalf("unexpected defaults: %v", bound)
	}

	// Zero, one, and many PRs fan-out expansion + deduplication by repo:number:head_sha.
	for _, n := range []int{0, 1, 3} {
		var items []any
		for i := 1; i <= n; i++ {
			items = append(items, map[string]any{"id": i, "head_sha": "sha-1"})
		}
		expanded, err := engine.ExpandFanOut("run-1", "review_pr", "review_pr", items, func(it any) (string, error) {
			m := it.(map[string]any)
			return fmt.Sprintf("flows/core:%d:%s", m["id"].(int), m["head_sha"].(string)), nil
		})
		if err != nil {
			t.Fatalf("expand %d: %v", n, err)
		}
		if len(expanded) != n {
			t.Fatalf("expected %d expanded instances, got %d", n, len(expanded))
		}
	}

	// Duplicate clock ticks, overlap, DST transition, and sleeping desktop / live daemon.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	sched := schedule.NewScheduler()
	if _, err := sched.Put(schedule.Schedule{
		ID:            "sched-review",
		WorkflowID:    "review-open-prs",
		Cron:          "0 9 * * *",
		Timezone:      "America/New_York",
		InputBindings: map[string]any{"repository": "flows/core"},
		Enabled:       true,
		OverlapRule:   schedule.OverlapForbid,
	}, def); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	launches := 0
	active := false
	createRoot := func(_ context.Context, _ string, _ schedule.Schedule) (string, error) {
		launches++
		return fmt.Sprintf("run-%d", launches), nil
	}
	isActive := func(string) bool { return active }

	// Spring-forward DST day in New York: 2026-03-08 09:00 EDT.
	tick1 := time.Date(2026, 3, 8, 9, 0, 0, 0, loc)
	out1, err := sched.Evaluate(context.Background(), tick1, isActive, createRoot)
	if err != nil || len(out1) != 1 || launches != 1 {
		t.Fatalf("expected 1 launch on first tick, got out=%+v launches=%d err=%v", out1, launches, err)
	}
	// Duplicate clock tick at same time does not launch again.
	if _, err := sched.Evaluate(context.Background(), tick1, isActive, createRoot); err != nil || launches != 1 {
		t.Fatalf("expected duplicate tick suppressed, got launches=%d err=%v", launches, err)
	}
	// Overlap while active run exists is skipped under OverlapForbid.
	active = true
	tick2 := tick1.Add(24 * time.Hour)
	if _, err := sched.Evaluate(context.Background(), tick2, isActive, createRoot); err != nil || launches != 1 {
		t.Fatalf("expected overlapping tick skipped, got launches=%d err=%v", launches, err)
	}
}
