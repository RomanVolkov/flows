package launcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/romanvolkov/flows/internal/workflow"
)

type FieldControl = workflow.FieldControl
type WorkflowForm = workflow.WorkflowForm

func NewWorkflowForm(def *workflow.Definition, latestRunID string) WorkflowForm {
	return workflow.NewWorkflowForm(def, latestRunID)
}

// WorkflowLaunchResult is returned on successful validation and dispatch.
type WorkflowLaunchResult struct {
	RequestID      string         `json:"request_id"`
	RunID          string         `json:"run_id"`
	FirstReachable []string       `json:"first_reachable"`
	ResolvedInputs map[string]any `json:"resolved_inputs"`
}

// WorkflowLauncher deduplicates launch requests by RequestID and dispatches
// the first reachable steps.
type WorkflowLauncher struct {
	mu       sync.Mutex
	launched map[string]WorkflowLaunchResult
	seq      int
}

// NewWorkflowLauncher creates a WorkflowLauncher.
func NewWorkflowLauncher() *WorkflowLauncher {
	return &WorkflowLauncher{
		launched: make(map[string]WorkflowLaunchResult),
	}
}

// Launch validates inputs, deduplicates by requestID, creates a run root, and
// returns its first reachable steps.
func (l *WorkflowLauncher) Launch(_ context.Context, def *workflow.Definition, form *WorkflowForm, values map[string]any, requestID string) (WorkflowLaunchResult, error) {
	if requestID == "" {
		return WorkflowLaunchResult{}, fmt.Errorf("request_id is required")
	}
	l.mu.Lock()
	if prev, ok := l.launched[requestID]; ok {
		l.mu.Unlock()
		return prev, nil
	}
	l.mu.Unlock()

	resolved, ok := form.Validate(def, values)
	if !ok {
		return WorkflowLaunchResult{}, fmt.Errorf("workflow input validation failed")
	}

	var first []string
	for _, id := range def.NodeOrder {
		ns := def.Nodes[id]
		if len(ns.Needs) == 0 {
			first = append(first, id)
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.launched[requestID]; ok {
		return prev, nil
	}
	l.seq++
	res := WorkflowLaunchResult{
		RequestID:      requestID,
		RunID:          fmt.Sprintf("run-%s-%d", def.ID, l.seq),
		FirstReachable: first,
		ResolvedInputs: resolved,
	}
	l.launched[requestID] = res
	return res, nil
}
