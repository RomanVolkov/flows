// Package editor implements the template canvas document, preview builder,
// and round-trip visual-to-YAML editing.
package editor

import (
	"github.com/romanvolkov/flows/desktop/canvas"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ContextMode distinguishes live execution inspection from template editing.
type ContextMode string

const (
	ModeExecution ContextMode = "execution"
	ModeTemplate  ContextMode = "template"
)

// TemplateDocument represents an opened workflow definition (live or pinned to an older run).
type TemplateDocument struct {
	DefinitionID  string                     `json:"definition_id"`
	Version       string                     `json:"version"`
	SourcePath    string                     `json:"source_path"`
	PinnedToRunID string                     `json:"pinned_to_run_id,omitempty"`
	BannerLabel   string                     `json:"banner_label"`
	Compiled      *workflow.CompiledWorkflow `json:"compiled"`
	Snapshot      *workflow.LaunchSnapshot   `json:"snapshot,omitempty"`
}

// EditorSession manages switching between the shared execution canvas and
// independent template views without losing the execution viewport.
type EditorSession struct {
	Mode            ContextMode              `json:"mode"`
	ExecutionCamera canvas.Camera            `json:"execution_camera"`
	TemplateCameras map[string]canvas.Camera `json:"template_cameras"`
	ActiveTemplate  *TemplateDocument        `json:"active_template,omitempty"`
}

// NewEditorSession creates an EditorSession starting in execution mode.
func NewEditorSession() *EditorSession {
	return &EditorSession{
		Mode:            ModeExecution,
		ExecutionCamera: canvas.DefaultCamera(),
		TemplateCameras: make(map[string]canvas.Camera),
	}
}

// OpenTemplate switches to template mode while preserving ExecutionCamera.
func (s *EditorSession) OpenTemplate(doc TemplateDocument) {
	if doc.BannerLabel == "" {
		if doc.PinnedToRunID != "" {
			doc.BannerLabel = "TEMPLATE (Pinned to " + doc.PinnedToRunID + "): " + doc.DefinitionID
		} else {
			doc.BannerLabel = "TEMPLATE: " + doc.DefinitionID
		}
	}
	s.Mode = ModeTemplate
	s.ActiveTemplate = &doc
	if _, ok := s.TemplateCameras[doc.DefinitionID]; !ok {
		s.TemplateCameras[doc.DefinitionID] = canvas.DefaultCamera()
	}
}

// ActiveTemplateCamera returns a pointer to the current template's independent camera.
func (s *EditorSession) ActiveTemplateCamera() *canvas.Camera {
	if s.ActiveTemplate == nil {
		return nil
	}
	cam := s.TemplateCameras[s.ActiveTemplate.DefinitionID]
	return &cam
}

// SaveTemplateCamera stores updated camera state for the active template.
func (s *EditorSession) SaveTemplateCamera(cam canvas.Camera) {
	if s.ActiveTemplate != nil {
		s.TemplateCameras[s.ActiveTemplate.DefinitionID] = cam
	}
}

// ReturnToExecution restores execution mode and its preserved viewport.
func (s *EditorSession) ReturnToExecution() canvas.Camera {
	s.Mode = ModeExecution
	s.ActiveTemplate = nil
	return s.ExecutionCamera
}
