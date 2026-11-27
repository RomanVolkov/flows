package workflow

import (
	"fmt"
	"slices"
	"strings"
)

// FieldControl represents one typed input field in the workflow launch form.
type FieldControl struct {
	Description string `json:"description,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Control     string `json:"control,omitempty"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Value       any    `json:"value,omitempty"`
	Error       string `json:"error,omitempty"`
}

// WorkflowForm validates workflow inputs before creating a run root.
type WorkflowForm struct {
	WorkflowID  string         `json:"workflow_id"`
	LatestRunID string         `json:"latest_run_id,omitempty"`
	Fields      []FieldControl `json:"fields"`
}

// NewWorkflowForm builds typed form controls from def.Inputs.
func NewWorkflowForm(def *Definition, latestRunID string) WorkflowForm {
	var names []string
	for k := range def.Inputs {
		names = append(names, k)
	}
	slices.Sort(names)

	fields := make([]FieldControl, 0, len(names))
	for _, name := range names {
		spec := def.Inputs[name]
		fields = append(fields, FieldControl{
			Name:        name,
			Description: spec.Description,
			Placeholder: spec.Placeholder,
			Enum:        spec.Enum,
			Control:     fieldControl(name, spec),
			Type:        spec.Type,
			Required:    spec.Required,
			Default:     spec.Default,
			Value:       spec.Default,
		})
	}
	return WorkflowForm{
		WorkflowID:  def.ID,
		LatestRunID: latestRunID,
		Fields:      fields,
	}
}

func fieldControl(name string, spec InputSpec) string {
	if spec.Control != "" {
		return spec.Control
	}
	if spec.Type == "boolean" {
		return "toggle"
	}
	if len(spec.Enum) > 0 {
		if len(spec.Enum) <= 3 {
			return "segmented"
		}
		return "select"
	}
	name = strings.ToLower(name)
	folder := strings.Contains(name, "repo") || strings.Contains(name, "folder") || strings.Contains(name, "directory")
	switch spec.Type {
	case "array":
		if folder {
			return "repos"
		}
		return "array"
	case "number", "integer":
		return "number"
	case "string":
		if folder {
			return "folder"
		}
	}
	return "text"
}

// Validate checks all fields and returns field-level diagnostics without
// creating a run root on failure.
func (f *WorkflowForm) Validate(def *Definition, values map[string]any) (map[string]any, bool) {
	resolved, err := ResolveInputs(def, values)
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
