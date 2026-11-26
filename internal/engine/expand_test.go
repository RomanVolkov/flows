package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/engine"
)

func TestFiveGroupsParallelPlanningGatedImplementationAndFanOut(t *testing.T) {
	// 1. Five declared groups published together; group-1..4 depend on group-0@merged.
	var decls []engine.GroupDecl
	for i := range 5 {
		g := engine.GroupDecl{
			ID:   fmt.Sprintf("group-%d", i),
			Name: fmt.Sprintf("Group %d", i),
		}
		if i > 0 {
			g.DependsOn = []engine.DependencyEdge{{FromStepID: "group-0", Milestone: "merged"}}
		}
		decls = append(decls, g)
	}
	stepsList, edges := engine.PublishGroups("run-1", decls)
	if len(stepsList) != 15 {
		t.Fatalf("expected 15 steps (5 groups * 3), got %d", len(stepsList))
	}
	steps := make(map[string]*engine.LogicalStep, len(stepsList))
	for i := range stepsList {
		steps[stepsList[i].ID] = &stepsList[i]
	}

	// All 5 plan steps are ready immediately and can run in parallel.
	for i := range 5 {
		if steps[fmt.Sprintf("group-%d.plan", i)].Status != engine.StepReady {
			t.Fatalf("group-%d.plan should be ready immediately", i)
		}
		if steps[fmt.Sprintf("group-%d.implement", i)].Status != engine.StepWaiting {
			t.Fatalf("group-%d.implement should be waiting", i)
		}
	}

	// Complete group-1.plan: group-1.implement stays waiting on group-0@merged.
	ms := engine.NewMilestoneRegistry()
	steps["group-1.plan"].Status = engine.StepSucceeded
	newly := engine.EvaluateReadiness(steps, edges, ms)
	if len(newly) != 0 {
		t.Fatalf("group-1.implement must remain gated by group-0@merged, got %v", newly)
	}

	// Publish group-0@merged: now group-1.implement becomes ready!
	ms.Publish("group-0", "merged", time.Now())
	newly = engine.EvaluateReadiness(steps, edges, ms)
	if len(newly) != 1 || newly[0] != "group-1.implement" {
		t.Fatalf("expected group-1.implement ready, got %v", newly)
	}

	// 2. Zero-item fan-out succeeds empty.
	empty, err := engine.ExpandFanOut("run-1", "review", "review", nil, func(a any) (string, error) { return "", nil })
	if err != nil || len(empty) != 0 {
		t.Fatalf("zero-item fan-out failed: %v %v", empty, err)
	}

	// 3. Multi-item fan-out preserves original order on join even if finished out of order.
	items := []any{"pr-10", "pr-20", "pr-30"}
	children, err := engine.ExpandFanOut("run-1", "review", "review", items, func(a any) (string, error) {
		return a.(string), nil
	})
	if err != nil || len(children) != 3 {
		t.Fatalf("ExpandFanOut failed: %v", err)
	}
	// Finish in reverse order.
	children[2].Outputs = map[string]any{"id": "pr-30"}
	children[0].Outputs = map[string]any{"id": "pr-10"}
	children[1].Outputs = map[string]any{"id": "pr-20"}
	joined := engine.JoinFanOut([]engine.LogicalStep{children[2], children[0], children[1]})
	if joined[0]["id"] != "pr-10" || joined[1]["id"] != "pr-20" || joined[2]["id"] != "pr-30" {
		t.Fatalf("fan-out join did not preserve input order: %+v", joined)
	}

	// 4. Repeat exhaustion -> needs_attention.
	next, done, needsAttn := engine.NextRepeatOccurrence("run-1", "loop", "review", 1, 3, false)
	if done || needsAttn || next.Iteration != 2 || next.OccurrenceID != "loop#iter-2" {
		t.Fatalf("unexpected next repeat: %+v done=%v attn=%v", next, done, needsAttn)
	}
	_, _, needsAttn = engine.NextRepeatOccurrence("run-1", "loop", "review", 3, 3, false)
	if !needsAttn {
		t.Fatalf("expected repeat exhaustion to trigger needs_attention")
	}
}

func TestDurableWatchTimerRestartAndCancellation(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0)

	ws1, err := engine.OpenWatchStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ws1.StartWatch("w-1", 10*time.Minute, now)
	if err != nil || st.WorkerSlotHeld {
		t.Fatalf("StartWatch: st=%+v err=%v", st, err)
	}

	st, err = ws1.RecordPoll("w-1", 1, engine.ObservationResult{
		Done:   false,
		Cursor: "cur-42",
		Data:   json.RawMessage(`{"checks":"pending"}`),
	}, 30*time.Second, now)
	if err != nil || st.Cursor != "cur-42" || st.Ordinal != 2 || st.WorkerSlotHeld {
		t.Fatalf("RecordPoll: st=%+v err=%v", st, err)
	}

	// Simulate daemon restart: reopen WatchStore from disk.
	ws2, err := engine.OpenWatchStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if due := ws2.DueWatches(now.Add(10 * time.Second)); len(due) != 0 {
		t.Fatalf("watch should not be due before interval")
	}
	due := ws2.DueWatches(now.Add(30 * time.Second))
	if len(due) != 1 || due[0].Cursor != "cur-42" || due[0].Ordinal != 2 {
		t.Fatalf("expected restored watch due with cur-42/ord-2, got %+v", due)
	}

	// Cancel watch and verify obsolete poll response is ignored.
	if err := ws2.Cancel("w-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws2.RecordPoll("w-1", 2, engine.ObservationResult{Done: true}, 30*time.Second, now.Add(35*time.Second)); !errors.Is(err, engine.ErrObsoleteWatchPoll) {
		t.Fatalf("expected ErrObsoleteWatchPoll after cancel, got %v", err)
	}
}
