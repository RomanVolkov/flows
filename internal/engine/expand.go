// Package engine implements workflow graph expansion, milestone readiness,
// bounded repetition, fan-out joins, and durable watch timers.
package engine

import (
	"fmt"
	"slices"
	"strconv"
)

// StepStatus tracks the logical lifecycle of a materialized step.
type StepStatus string

const (
	StepWaiting        StepStatus = "waiting"
	StepReady          StepStatus = "ready"
	StepRunning        StepStatus = "running"
	StepWaitingWatch   StepStatus = "waiting_watch"
	StepNeedsAttention StepStatus = "needs_attention"
	StepSucceeded      StepStatus = "succeeded"
	StepFailed         StepStatus = "failed"
	StepSkipped        StepStatus = "skipped"
	StepCancelled      StepStatus = "cancelled"
)

// LogicalStep is a materialized step (distinct from its execution attempts).
type LogicalStep struct {
	ID           string         `json:"id"`
	RunID        string         `json:"run_id"`
	ParentID     string         `json:"parent_id,omitempty"`
	Kind         string         `json:"kind"` // "group" | "session" | "tool" | "watch"
	Name         string         `json:"name"`
	Status       StepStatus     `json:"status"`
	OccurrenceID string         `json:"occurrence_id,omitempty"`
	Iteration    int            `json:"iteration,omitempty"`
	ItemKey      string         `json:"item_key,omitempty"`
	ItemIndex    int            `json:"item_index,omitempty"`
	ConditionMet *bool          `json:"condition_met,omitempty"`
	Outputs      map[string]any `json:"outputs,omitempty"`
}

// DependencyEdge links a prerequisite step (or its named milestone) to a consumer step.
type DependencyEdge struct {
	FromStepID string `json:"from_step_id"`
	ToStepID   string `json:"to_step_id"`
	Milestone  string `json:"milestone,omitempty"`
}

// GroupDecl declares a group root and its milestone dependencies.
type GroupDecl struct {
	ID        string
	Name      string
	DependsOn []DependencyEdge // e.g. {FromStepID: "group-a", Milestone: "merged"}
}

// PublishGroups materializes all declared group roots and their milestone edges
// together, plus each group's planning step (ready immediately) and implementation
// step (gated by group prerequisite milestones).
func PublishGroups(runID string, groups []GroupDecl) ([]LogicalStep, []DependencyEdge) {
	steps := make([]LogicalStep, 0, len(groups)*3)
	var edges []DependencyEdge

	for _, g := range groups {
		steps = append(steps, LogicalStep{
			ID:     g.ID,
			RunID:  runID,
			Kind:   "group",
			Name:   g.Name,
			Status: StepRunning,
		})
		planID := g.ID + ".plan"
		implID := g.ID + ".implement"
		steps = append(steps,
			LogicalStep{
				ID:       planID,
				RunID:    runID,
				ParentID: g.ID,
				Kind:     "session",
				Name:     "plan",
				Status:   StepReady,
			},
			LogicalStep{
				ID:       implID,
				RunID:    runID,
				ParentID: g.ID,
				Kind:     "session",
				Name:     "implement",
				Status:   StepWaiting,
			},
		)
		// Implementation waits on own plan AND any declared cross-group milestones.
		edges = append(edges, DependencyEdge{FromStepID: planID, ToStepID: implID})
		for _, dep := range g.DependsOn {
			edges = append(edges, DependencyEdge{
				FromStepID: dep.FromStepID,
				ToStepID:   implID,
				Milestone:  dep.Milestone,
			})
		}
	}
	return steps, edges
}

// ExpandFanOut creates one LogicalStep per item with unique stable keys.
// Zero items returns nil, nil (caller treats the fan-out as empty success).
func ExpandFanOut(runID, parentID, baseName string, items []any, keyFn func(any) (string, error)) ([]LogicalStep, error) {
	if len(items) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]LogicalStep, 0, len(items))
	for i, item := range items {
		k, err := keyFn(item)
		if err != nil {
			return nil, fmt.Errorf("fan-out item %d key: %w", i, err)
		}
		if k == "" {
			return nil, fmt.Errorf("fan-out item %d produced empty key", i)
		}
		if seen[k] {
			return nil, fmt.Errorf("duplicate fan-out key %q", k)
		}
		seen[k] = true
		out = append(out, LogicalStep{
			ID:           parentID + "[" + k + "]",
			RunID:        runID,
			ParentID:     parentID,
			Kind:         "session",
			Name:         baseName,
			Status:       StepReady,
			ItemKey:      k,
			ItemIndex:    i,
			OccurrenceID: parentID + "#" + k,
		})
	}
	return out, nil
}

// JoinFanOut collects completed fan-out outputs in original item index order,
// independent of the order in which items finished.
func JoinFanOut(children []LogicalStep) []map[string]any {
	ordered := slices.Clone(children)
	slices.SortFunc(ordered, func(a, b LogicalStep) int {
		return a.ItemIndex - b.ItemIndex
	})
	res := make([]map[string]any, 0, len(ordered))
	for _, ch := range ordered {
		res = append(res, ch.Outputs)
	}
	return res
}

// NextRepeatOccurrence evaluates a bounded repetition step and either finishes,
// creates a fresh acyclic occurrence for the next iteration, or enters NeedsAttention.
func NextRepeatOccurrence(runID, baseStepID, name string, currentIter, maxIter int, conditionSatisfied bool) (next *LogicalStep, done bool, needsAttention bool) {
	if conditionSatisfied {
		return nil, true, false
	}
	if currentIter >= maxIter {
		return nil, false, true
	}
	nextIter := currentIter + 1
	occID := baseStepID + "#iter-" + strconv.Itoa(nextIter)
	return &LogicalStep{
		ID:           occID,
		RunID:        runID,
		Kind:         "session",
		Name:         name,
		Status:       StepReady,
		OccurrenceID: occID,
		Iteration:    nextIter,
	}, false, false
}
