package schedule_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/schedule"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestScheduleDuplicateTicksSleepWakeDSTAndRequiredInputs(t *testing.T) {
	ctx := context.Background()

	def, diags := workflow.ParseBytes("workflow.yaml", []byte(`schema: flows/v1
id: review-open-prs
version: 1.0.0
inputs:
  repository: {type: string, required: true}
  reviewer: {type: string, default: codex}
nodes:
  discover:
    uses: core.command
`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}

	s := schedule.NewScheduler()

	// 1. Reject enabling without required input, reporting exact missing key.
	missing, err := s.Put(schedule.Schedule{
		ID:         "sched-1",
		WorkflowID: "review-open-prs",
		Cron:       "0 * * * *",
		Timezone:   "America/New_York",
		Enabled:    true,
	}, def)
	if err == nil || len(missing) != 1 || missing[0] != "repository" {
		t.Fatalf("expected missing=[repository], got missing=%v err=%v", missing, err)
	}

	// 2. Enable with bound required input.
	t0 := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC) // 01:30 EST, right before US spring-forward DST at 02:00 EST -> 03:00 EDT!
	_, err = s.Put(schedule.Schedule{
		ID:               "sched-1",
		WorkflowID:       "review-open-prs",
		Cron:             "30 2 * * *", // 02:30 local (skipped on spring-forward day, next day 02:30 EDT)
		Timezone:         "America/New_York",
		InputBindings:    map[string]any{"repository": "flows/core"},
		Enabled:          true,
		LastOccurrenceAt: t0.UnixNano(),
	}, def)
	if err != nil {
		t.Fatal(err)
	}

	// DST spring-forward check: 02:30 local does not exist on 2026-03-08, so next tick is 2026-03-09 02:30 EDT (06:30 UTC).
	ny, _ := time.LoadLocation("America/New_York")
	cronDST, _ := schedule.ParseCron("30 2 * * *")
	nextDST := cronDST.Next(t0, ny)
	if nextDST.Format(time.RFC3339) != "2026-03-09T06:30:00Z" {
		t.Fatalf("DST spring-forward next = %s, want 2026-03-09T06:30:00Z", nextDST.Format(time.RFC3339))
	}

	// 3. Sleep/wake catch-up combines missed hourly ticks into 1 occurrence + duplicate tick deduplicated.
	s2 := schedule.NewScheduler()
	_, err = s2.Put(schedule.Schedule{
		ID:               "sched-hourly",
		WorkflowID:       "review-open-prs",
		Cron:             "0 * * * *",
		Timezone:         "UTC",
		InputBindings:    map[string]any{"repository": "flows/core"},
		Enabled:          true,
		LastOccurrenceAt: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).UnixNano(),
	}, def)
	if err != nil {
		t.Fatal(err)
	}

	// Wake up 3 hours later -> 3 missed ticks combined into 1 catch-up (Combined == 2).
	wake := time.Date(2026, 6, 1, 13, 5, 0, 0, time.UTC)
	seq := 0
	out1, err := s2.Evaluate(ctx, wake, func(string) bool { return false }, func(_ context.Context, _ string, _ schedule.Schedule) (string, error) {
		seq++
		return fmt.Sprintf("run-%d", seq), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var triggered int
	for _, o := range out1 {
		if o.ScheduleID == "sched-hourly" {
			if o.Status != "triggered" || o.Combined != 2 {
				t.Fatalf("expected 1 catch-up with Combined=2, got %+v", o)
			}
			triggered++
		}
	}
	if triggered != 1 || seq != 1 {
		t.Fatalf("expected exactly 1 triggered run after sleep, got %d", seq)
	}

	// Duplicate tick at same instant produces 0 new runs.
	out2, err := s2.Evaluate(ctx, wake, func(string) bool { return false }, func(_ context.Context, _ string, _ schedule.Schedule) (string, error) {
		seq++
		return fmt.Sprintf("run-%d", seq), nil
	})
	if err != nil || seq != 1 {
		t.Fatalf("duplicate tick created extra run: seq=%d out=%+v err=%v", seq, out2, err)
	}
}
