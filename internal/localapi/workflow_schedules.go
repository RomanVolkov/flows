package localapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/schedule"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

type scheduleRequest struct {
	ID            string         `json:"id"`
	WorkflowID    string         `json:"workflow_id"`
	Cron          string         `json:"cron"`
	Timezone      string         `json:"timezone"`
	InputBindings map[string]any `json:"input_bindings"`
	Enabled       bool           `json:"enabled"`
}

func (r *workflowRunner) serveSchedules(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v1/schedules" && req.Method == http.MethodGet {
		values, err := r.state.ListSchedules(req.Context())
		if err != nil {
			writeError(w, 500, "schedule_list_failed", "could not load schedules")
			return
		}
		views := make([]map[string]any, 0, len(values))
		for _, value := range values {
			def := r.workflowDefinition(value.WorkflowID)
			view := describeSchedule(value, def, time.Now())
			views = append(views, map[string]any{"schedule": value, "view": view})
		}
		writeJSON(w, 200, views)
		return
	}
	if req.URL.Path == "/v1/schedules" || req.URL.Path == "/v1/schedules/put" {
		if !method(w, req, http.MethodPost) {
			return
		}
		var input scheduleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, MaxBodyBytes)).Decode(&input); err != nil || input.WorkflowID == "" {
			writeError(w, 400, "invalid_schedule", "workflow_id and valid schedule settings are required")
			return
		}
		def, entry := r.workflowDefinitionAndEntry(input.WorkflowID)
		if def == nil || entry.ID == "" {
			writeError(w, 422, "workflow_unavailable", "select a ready workflow")
			return
		}
		resolved, err := workflow.ResolveInputs(def, input.InputBindings)
		if err != nil {
			writeError(w, 422, "invalid_schedule_inputs", err.Error())
			return
		}
		input.ID = strings.TrimSpace(input.ID)
		if input.ID == "" {
			input.ID = "schedule-" + rand.Text()[:10]
		}
		if input.Timezone == "" {
			input.Timezone = "Local"
		}
		value := schedule.Schedule{ID: input.ID, WorkflowID: input.WorkflowID, DefinitionRevision: entry.Digest, Cron: input.Cron, Timezone: input.Timezone,
			InputBindings: resolved, Enabled: input.Enabled, OverlapRule: schedule.OverlapForbid}
		if _, err := r.scheduler.Put(value, def); err != nil {
			writeError(w, 422, "invalid_schedule", err.Error())
			return
		}
		if err := r.state.PutSchedule(req.Context(), value); err != nil {
			r.scheduler.Remove(value.ID)
			writeError(w, 500, "schedule_save_failed", "could not save schedule")
			return
		}
		writeJSON(w, 200, value)
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/v1/schedules/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, 404, "not_found", "schedule not found")
		return
	}
	if !method(w, req, http.MethodDelete) {
		return
	}
	r.scheduler.Remove(id)
	if err := r.state.DeleteSchedule(req.Context(), id); err != nil {
		writeError(w, 500, "schedule_delete_failed", "could not delete schedule")
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func describeSchedule(value schedule.Schedule, def *workflow.Definition, now time.Time) map[string]any {
	view := map[string]any{"id": value.ID, "workflow_id": value.WorkflowID, "enabled": value.Enabled}
	if cron, err := schedule.ParseCron(value.Cron); err == nil {
		view["plain_language"] = cron.Describe()
		loc := time.Local
		if value.Timezone != "" && value.Timezone != "Local" {
			if parsed, err := time.LoadLocation(value.Timezone); err == nil {
				loc = parsed
			}
		}
		if next := cron.Next(now, loc); !next.IsZero() {
			view["next_tick_utc"] = next.UTC().Format(time.RFC3339)
		}
	}
	if def != nil {
		missing, _ := schedule.ValidateBindings(def, value.InputBindings)
		view["missing_inputs"] = missing
	}
	return view
}

func (r *workflowRunner) workflowDefinition(id string) *workflow.Definition {
	def, _ := r.workflowDefinitionAndEntry(id)
	return def
}
func (r *workflowRunner) workflowDefinitionAndEntry(id string) (*workflow.Definition, struct{ ID, Digest string }) {
	r.library.mu.RLock()
	defer r.library.mu.RUnlock()
	entry := struct{ ID, Digest string }{}
	for _, candidate := range r.library.index.Entries {
		if candidate.ID == id && candidate.Kind == "workflow" && candidate.Status == "ready" {
			entry.ID, entry.Digest = candidate.ID, candidate.Digest
			break
		}
	}
	if snap := r.library.prepared[id]; snap != nil && entry.ID != "" {
		return snap.Compiled.Definition, entry
	}
	return nil, entry
}

func (r *workflowRunner) runSchedules() {
	ctx := r.ctx
	load := func() {
		values, err := r.state.ListSchedules(ctx)
		if err != nil {
			return
		}
		for _, value := range values {
			if def := r.workflowDefinition(value.WorkflowID); def != nil {
				_, _ = r.scheduler.Put(value, def)
			}
		}
	}
	load()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			load()
			outcomes, err := r.scheduler.Evaluate(ctx, now, func(runID string) bool {
				ws, e := r.state.Snapshot(ctx)
				if e != nil {
					return false
				}
				for _, n := range ws.Nodes {
					if n.RunID == runID && n.Status != store.Succeeded && n.Status != store.Failed && n.Status != store.Cancelled && n.Status != store.Skipped {
						return true
					}
				}
				return false
			}, func(_ context.Context, occurrence string, sched schedule.Schedule) (string, error) {
				def, entry := r.workflowDefinitionAndEntry(sched.WorkflowID)
				if def == nil {
					return "", fmt.Errorf("workflow %s is not ready", sched.WorkflowID)
				}
				req := workflowLaunchRequest{RequestID: "scheduled:" + occurrence, WorkflowID: sched.WorkflowID, ExpectedDigest: entry.Digest, Inputs: sched.InputBindings}
				spec, snap, e := r.prepareLaunch(req)
				if e != nil {
					return "", e
				}
				run, created, e := r.state.CreateWorkflowRun(ctx, spec)
				if e == nil && created {
					r.start(run, spec, snap, req)
				}
				return run.RunID, e
			})
			if err != nil {
				continue
			}
			_ = outcomes
			for _, value := range r.scheduler.List() {
				_ = r.state.PutSchedule(ctx, value)
			}
		}
	}
}
