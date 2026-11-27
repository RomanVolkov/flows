package localapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/workflow"
)

type workflowLaunchRequest struct {
	RequestID      string         `json:"request_id"`
	WorkflowID     string         `json:"workflow_id"`
	ExpectedDigest string         `json:"expected_digest"`
	WorkDir        string         `json:"work_dir"`
	Inputs         map[string]any `json:"inputs"`
}

type workflowSummary struct {
	library.IndexEntry
	Description string                `json:"description,omitempty"`
	Form        workflow.WorkflowForm `json:"form"`
}

func (r *workflowRunner) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v1/workflows" {
		if !method(w, req, http.MethodGet) {
			return
		}
		r.library.trigger()
		r.library.mu.RLock()
		entries := r.library.index.Entries
		prepared := r.library.prepared
		preparing := r.library.lastScan == nil
		r.library.mu.RUnlock()
		workflows := make([]workflowSummary, 0)
		for _, entry := range entries {
			preparing = preparing || entry.Status == "preparing"
			if entry.Kind != library.KindWorkflow || entry.Status != "ready" {
				continue
			}
			if snapshot := prepared[entry.ID]; snapshot != nil {
				def := snapshot.Compiled.Definition
				workflows = append(workflows, workflowSummary{
					IndexEntry: entry, Description: def.Description, Form: workflow.NewWorkflowForm(def, ""),
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflows": workflows, "preparing": preparing})
		return
	}
	if !method(w, req, http.MethodPost) {
		return
	}
	var input workflowLaunchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, MaxBodyBytes))
	if err := decoder.Decode(&input); err != nil || input.RequestID == "" || input.WorkflowID == "" || input.ExpectedDigest == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "request_id, workflow_id, and expected_digest are required")
		return
	}
	ctx := req.Context()
	if existing, err := r.state.WorkflowRunByRequest(ctx, input.RequestID); err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	spec, compiled, err := r.prepareLaunch(input)
	if err != nil {
		if errors.Is(err, errStaleWorkflow) {
			r.library.trigger()
			writeError(w, http.StatusConflict, "workflow_changed", err.Error())
		} else {
			writeError(w, http.StatusUnprocessableEntity, "invalid_workflow_run", err.Error())
		}
		return
	}
	run, created, err := r.state.CreateWorkflowRun(ctx, spec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "launch_failed", err.Error())
		return
	}
	if created {
		r.start(run, spec, compiled, input)
	}
	writeJSON(w, http.StatusOK, run)
}
