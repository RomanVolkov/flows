package sdk

import "encoding/json"

const (
	// ProtocolVersion is the versioned stdio frame protocol between flowsd and plugins.
	ProtocolVersion = 1
	// MaxFrameBytes bounds a single NDJSON frame on stdin/stdout.
	MaxFrameBytes = 1 << 20
)

// Manifest describes a plugin executable's operations and metadata.
type Manifest struct {
	Protocol    int         `json:"protocol"`
	ID          string      `json:"id"`
	Version     string      `json:"version"`
	Owner       string      `json:"owner,omitempty"`
	Description string      `json:"description,omitempty"`
	Operations  []Operation `json:"operations"`
}

// Operation describes one callable plugin operation.
type Operation struct {
	ID           string          `json:"id"`
	Description  string          `json:"description"`
	ReadOnly     bool            `json:"read_only,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Milestones   []string        `json:"milestones,omitempty"`
}

// Request is sent from the engine to the plugin over stdin.
type Request struct {
	Protocol       int             `json:"protocol"`
	RequestID      string          `json:"request_id"`
	Kind           string          `json:"kind"` // "describe" | "invoke" | "cancel"
	Operation      string          `json:"operation,omitempty"`
	AttemptID      string          `json:"attempt_id,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	CallbackSocket string          `json:"callback_socket,omitempty"`
	CallbackToken  string          `json:"callback_token,omitempty"`
	Cursor         string          `json:"cursor,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
}

// FrameError is a structured error returned by a plugin.
type FrameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *FrameError) Error() string { return e.Message }

// Observation is returned by a read-only watch poll operation.
type Observation struct {
	Done   bool            `json:"done"`
	Cursor string          `json:"cursor,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Frame is sent from the plugin to the engine over stdout.
type Frame struct {
	Protocol    int             `json:"protocol"`
	RequestID   string          `json:"request_id"`
	Kind        string          `json:"kind"` // "manifest" | "progress" | "milestone" | "result" | "observation" | "error"
	Message     string          `json:"message,omitempty"`
	Percent     float64         `json:"percent,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Observation *Observation    `json:"observation,omitempty"`
	Manifest    *Manifest       `json:"manifest,omitempty"`
	Error       *FrameError     `json:"error,omitempty"`
}
