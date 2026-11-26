package schedule

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/workflow"
)

// OverlapRule controls concurrent runs of the same schedule.
type OverlapRule string

const (
	OverlapForbid OverlapRule = "forbid" // default: one active run per schedule
	OverlapAllow  OverlapRule = "allow"
)

// Schedule defines a repeatable workflow trigger with bound inputs.
type Schedule struct {
	ID                 string         `json:"id"`
	WorkflowID         string         `json:"workflow_id"`
	DefinitionRevision string         `json:"definition_revision"`
	Cron               string         `json:"cron"`
	Timezone           string         `json:"timezone"`
	InputBindings      map[string]any `json:"input_bindings,omitempty"`
	Enabled            bool           `json:"enabled"`
	OverlapRule        OverlapRule    `json:"overlap_rule"`
	LastOccurrenceKey  string         `json:"last_occurrence_key,omitempty"`
	LastOccurrenceAt   int64          `json:"last_occurrence_at,omitempty"`
	LastResult         string         `json:"last_result,omitempty"`
	ActiveRunID        string         `json:"active_run_id,omitempty"`
	SkippedCount       int            `json:"skipped_count"`
	CombinedCount      int            `json:"combined_count"`
}

// ValidateBindings checks that every required workflow input without a default
// is covered by bindings, returning exact missing keys on failure.
func ValidateBindings(def *workflow.Definition, bindings map[string]any) ([]string, error) {
	if def == nil {
		return nil, fmt.Errorf("workflow definition is nil")
	}
	var missing []string
	for name, spec := range def.Inputs {
		if !spec.Required || spec.Default != nil {
			continue
		}
		if v, ok := bindings[name]; !ok || v == nil {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		return missing, fmt.Errorf("missing required launch inputs: %v", missing)
	}
	return nil, nil
}

// TickOutcome describes the result of evaluating one schedule at now.
type TickOutcome struct {
	ScheduleID    string `json:"schedule_id"`
	OccurrenceKey string `json:"occurrence_key,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	Status        string `json:"status"` // "triggered" | "skipped_overlap" | "duplicate" | "not_due"
	Combined      int    `json:"combined,omitempty"`
}

// Scheduler evaluates enabled schedules with unique occurrence keys, overlap
// enforcement, and single-catch-up after sleep.
type Scheduler struct {
	mu               sync.Mutex
	seenOccurrences  map[string]bool
	schedules        map[string]*Schedule
}

// NewScheduler creates a Scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		seenOccurrences: make(map[string]bool),
		schedules:       make(map[string]*Schedule),
	}
}

// Put registers or updates sched after validating its cron and timezone.
func (s *Scheduler) Put(sched Schedule, def *workflow.Definition) ([]string, error) {
	if _, err := ParseCron(sched.Cron); err != nil {
		return nil, err
	}
	if sched.Timezone != "" && sched.Timezone != "Local" {
		if _, err := time.LoadLocation(sched.Timezone); err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", sched.Timezone, err)
		}
	}
	if sched.Enabled && def != nil {
		if missing, err := ValidateBindings(def, sched.InputBindings); err != nil {
			return missing, err
		}
	}
	if sched.OverlapRule == "" {
		sched.OverlapRule = OverlapForbid
	}
	s.mu.Lock()
	cp := sched
	s.schedules[sched.ID] = &cp
	s.mu.Unlock()
	return nil, nil
}

// Evaluate processes all enabled schedules at now, enforcing unique occurrence keys,
// single-catch-up after sleep, and overlap rules.
func (s *Scheduler) Evaluate(
	ctx context.Context,
	now time.Time,
	isActive func(runID string) bool,
	createRoot func(ctx context.Context, occKey string, sched Schedule) (string, error),
) ([]TickOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var outcomes []TickOutcome
	for _, sched := range s.schedules {
		if !sched.Enabled {
			continue
		}
		cron, err := ParseCron(sched.Cron)
		if err != nil {
			continue
		}
		loc := time.Local
		if sched.Timezone != "" && sched.Timezone != "Local" {
			if l, err := time.LoadLocation(sched.Timezone); err == nil {
				loc = l
			}
		}

		anchor := now.Add(-time.Minute)
		if sched.LastOccurrenceAt > 0 {
			anchor = time.Unix(0, sched.LastOccurrenceAt)
		}

		// Collect missed ticks up to now; combine missed ticks into at most ONE catch-up occurrence.
		var latestDue time.Time
		missed := 0
		cur := anchor
		for {
			next := cron.Next(cur, loc)
			if next.IsZero() || next.After(now) {
				break
			}
			latestDue = next
			missed++
			cur = next
		}
		if missed == 0 {
			outcomes = append(outcomes, TickOutcome{ScheduleID: sched.ID, Status: "not_due"})
			continue
		}

		occKey := fmt.Sprintf("%s:%s", sched.ID, latestDue.UTC().Format(time.RFC3339))
		if s.seenOccurrences[occKey] || sched.LastOccurrenceKey == occKey {
			outcomes = append(outcomes, TickOutcome{ScheduleID: sched.ID, OccurrenceKey: occKey, Status: "duplicate"})
			continue
		}

		if sched.OverlapRule == OverlapForbid && sched.ActiveRunID != "" && isActive != nil && isActive(sched.ActiveRunID) {
			sched.SkippedCount++
			sched.LastOccurrenceKey = occKey
			sched.LastOccurrenceAt = latestDue.UnixNano()
			s.seenOccurrences[occKey] = true
			outcomes = append(outcomes, TickOutcome{ScheduleID: sched.ID, OccurrenceKey: occKey, Status: "skipped_overlap"})
			continue
		}

		combined := max(0, missed-1)
		sched.CombinedCount += combined

		runID, err := createRoot(ctx, occKey, *sched)
		if err != nil {
			return outcomes, err
		}
		s.seenOccurrences[occKey] = true
		sched.LastOccurrenceKey = occKey
		sched.LastOccurrenceAt = latestDue.UnixNano()
		sched.ActiveRunID = runID
		sched.LastResult = "triggered"

		outcomes = append(outcomes, TickOutcome{
			ScheduleID:    sched.ID,
			OccurrenceKey: occKey,
			RunID:         runID,
			Status:        "triggered",
			Combined:      combined,
		})
	}
	return outcomes, nil
}
