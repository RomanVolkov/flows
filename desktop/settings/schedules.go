package settings

import (
	"time"

	"github.com/romanvolkov/flows/internal/schedule"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ScheduleView models one schedule row in Settings -> Schedules.
type ScheduleView struct {
	ID            string   `json:"id"`
	WorkflowID    string   `json:"workflow_id"`
	Timezone      string   `json:"timezone"`
	PlainLanguage string   `json:"plain_language"`
	NextTickUTC   string   `json:"next_tick_utc,omitempty"`
	LastResult    string   `json:"last_result,omitempty"`
	SkippedCount  int      `json:"skipped_count"`
	CombinedCount int      `json:"combined_count"`
	Enabled       bool     `json:"enabled"`
	MissingInputs []string `json:"missing_inputs,omitempty"`
}

// BuildScheduleView formats schedule metadata and validates required inputs.
func BuildScheduleView(sched schedule.Schedule, def *workflow.Definition, now time.Time) ScheduleView {
	view := ScheduleView{
		ID:            sched.ID,
		WorkflowID:    sched.WorkflowID,
		Timezone:      sched.Timezone,
		LastResult:    sched.LastResult,
		SkippedCount:  sched.SkippedCount,
		CombinedCount: sched.CombinedCount,
		Enabled:       sched.Enabled,
	}
	if cron, err := schedule.ParseCron(sched.Cron); err == nil {
		view.PlainLanguage = cron.Describe()
		loc := time.Local
		if sched.Timezone != "" && sched.Timezone != "Local" {
			if l, lerr := time.LoadLocation(sched.Timezone); lerr == nil {
				loc = l
			}
		}
		if next := cron.Next(now, loc); !next.IsZero() {
			view.NextTickUTC = next.UTC().Format(time.RFC3339)
		}
	}
	if def != nil {
		missing, _ := schedule.ValidateBindings(def, sched.InputBindings)
		view.MissingInputs = missing
	}
	return view
}
