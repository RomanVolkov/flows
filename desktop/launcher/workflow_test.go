package launcher

import (
	"testing"

	"github.com/romanvolkov/flows/internal/workflow"
)

func TestWorkflowFormControls(t *testing.T) {
	def := &workflow.Definition{ID: "sample", Inputs: map[string]workflow.InputSpec{
		"provider":     {Type: "string", Enum: []any{"gitlab", "github"}, Description: "Hosting service"},
		"mode":         {Type: "string", Enum: []any{"one", "two", "three", "four"}},
		"approve":      {Type: "boolean"},
		"count":        {Type: "integer"},
		"repository":   {Type: "string"},
		"repositories": {Type: "array"},
		"tags":         {Type: "array"},
		"notes":        {Type: "string", Control: "multiline", Placeholder: "Optional"},
	}}
	expected := map[string]string{"provider": "segmented", "mode": "select", "approve": "toggle", "count": "number", "repository": "folder", "repositories": "repos", "tags": "array", "notes": "multiline"}
	form := NewWorkflowForm(def, "")
	for _, field := range form.Fields {
		if want := expected[field.Name]; field.Control != want {
			t.Errorf("%s control = %q, want %q", field.Name, field.Control, want)
		}
		if field.Name == "provider" && (field.Description != "Hosting service" || len(field.Enum) != 2) {
			t.Errorf("metadata missing: %+v", field)
		}
	}
}
