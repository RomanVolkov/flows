package launcher

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/romanvolkov/flows/internal/workflow"
)

// FieldControl represents one typed input field in the workflow launch form.
type FieldControl struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  any    `json:"default,omitempty"`
	Value    any    `json:"value,omitempty"`
	Error    string `json:"error,omitempty"`
}

// WorkflowForm validates workflow inputs before creating a run root.
type WorkflowForm struct {
	WorkflowID  string         `json:"workflow_id"`
	LatestRunID string         `json:"latest_run_id,omitempty"`
	Fields      []FieldControl `json:"fields"`
}

// NewWorkflowForm builds typed form controls from def.Inputs.
func NewWorkflowForm(def *workflow.Definition, latestRunID string) WorkflowForm {
	var names []string
	for k := range def.Inputs {
		names = append(names, k)
	}
	slices.Sort(names)

	fields := make([]FieldControl, 0, len(names))
	for _, name := range names {
		spec := def.Inputs[name]
		fields = append(fields, FieldControl{
			Name:     name,
			Type:     spec.Type,
			Required: spec.Required,
			Default:  spec.Default,
			Value:    spec.Default,
		})
	}
	return WorkflowForm{
		WorkflowID:  def.ID,
		LatestRunID: latestRunID,
		Fields:      fields,
	}
}

// Validate checks all fields and returns field-level diagnostics without
// creating a run root on failure.
func (f *WorkflowForm) Validate(def *workflow.Definition, values map[string]any) (map[string]any, bool) {
	resolved, err := workflow.ResolveInputs(def, values)
	if err == nil {
		for i := range f.Fields {
			f.Fields[i].Error = ""
			f.Fields[i].Value = resolved[f.Fields[i].Name]
		}
		return resolved, true
	}

	valid := true
	for i := range f.Fields {
		name := f.Fields[i].Name
		spec := def.Inputs[name]
		val, provided := values[name]
		if (!provided || val == nil) && spec.Default == nil && spec.Required {
			f.Fields[i].Error = fmt.Sprintf("missing required field %q", name)
			valid = false
		} else {
			f.Fields[i].Error = ""
		}
	}
	if valid {
		// Type error on a specific field.
		for i := range f.Fields {
			f.Fields[i].Error = err.Error()
		}
		valid = false
	}
	return nil, valid
}

// WorkflowLaunchResult is returned on successful validation and dispatch.
type WorkflowLaunchResult struct {
	RequestID       string         `json:"request_id"`
	RunID           string         `json:"run_id"`
	FirstReachable  []string       `json:"first_reachable"`
	ResolvedInputs  map[string]any `json:"resolved_inputs"`
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
