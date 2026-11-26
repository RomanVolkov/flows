// Package inspector manages the desktop workspace shell state, focus routing,
// node inspector, and detachable terminal dock ownership.
package inspector

import "fmt"

// FocusTarget identifies which pane currently holds keyboard focus.
type FocusTarget string

const (
	FocusCanvas    FocusTarget = "canvas"
	FocusSidebar   FocusTarget = "sidebar"
	FocusInspector FocusTarget = "inspector"
	FocusTerminal  FocusTarget = "terminal"
)

// NodeSummary describes a selected or inspected node.
type NodeSummary struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
}

// EmptySessionRoot returns the single root harness node for an empty session.
func EmptySessionRoot(runID, sessionID, harness string) NodeSummary {
	name := harness
	if name == "" {
		name = "session"
	}
	return NodeSummary{
		ID:        sessionID,
		RunID:     runID,
		Kind:      "session",
		Name:      name,
		Status:    "running",
		SessionID: sessionID,
	}
}

// TerminalDock tracks the docked Ghostty view and its input ownership.
type TerminalDock struct {
	Open       bool   `json:"open"`
	SessionID  string `json:"session_id,omitempty"`
	SocketPath string `json:"socket_path,omitempty"`
	InputOwner bool   `json:"input_owner"`
}

// ShellState models the top bar, reconnect banner, sidebar, canvas, inspector,
// terminal dock, selection summary, and footer.
type ShellState struct {
	Focus            FocusTarget   `json:"focus"`
	Connected        bool          `json:"connected"`
	ReconnectMessage string        `json:"reconnect_message,omitempty"`
	Selection        []NodeSummary `json:"selection,omitempty"`
	Dock             TerminalDock  `json:"dock"`
	WindowOpen       bool          `json:"window_open"`
}

// NewShellState returns the default workspace shell state.
func NewShellState() *ShellState {
	return &ShellState{
		Focus:      FocusCanvas,
		Connected:  true,
		WindowOpen: true,
	}
}

// OpenDock attaches the terminal dock to sessionID and claims input ownership.
func (s *ShellState) OpenDock(sessionID, socketPath string) {
	s.Dock = TerminalDock{
		Open:       true,
		SessionID:  sessionID,
		SocketPath: socketPath,
		InputOwner: true,
	}
	s.Focus = FocusTerminal
}

// CloseDock detaches the terminal dock and releases input ownership without
// stopping the underlying session host.
func (s *ShellState) CloseDock() {
	s.Dock.Open = false
	s.Dock.InputOwner = false
	if s.Focus == FocusTerminal {
		s.Focus = FocusCanvas
	}
}

// CloseWindow closes the desktop window while preserving the underlying session
// and releasing terminal input ownership.
func (s *ShellState) CloseWindow() {
	s.WindowOpen = false
	s.Dock.InputOwner = false
}

// ShowTerminalControls reports whether terminal-only controls should be visible.
func (s *ShellState) ShowTerminalControls() bool {
	return s.Dock.Open && s.Dock.SessionID != ""
}

// SetDisconnected updates the reconnect banner state.
func (s *ShellState) SetDisconnected(msg string) {
	s.Connected = false
	if msg == "" {
		msg = "Reconnecting to flowsd..."
	}
	s.ReconnectMessage = msg
}

// SetConnected clears the reconnect banner.
func (s *ShellState) SetConnected() {
	s.Connected = true
	s.ReconnectMessage = ""
}

// CycleFocus moves keyboard focus between sidebar, canvas, inspector, and
// (when open) the terminal dock.
func (s *ShellState) CycleFocus(forward bool) FocusTarget {
	order := []FocusTarget{FocusSidebar, FocusCanvas, FocusInspector}
	if s.ShowTerminalControls() {
		order = append(order, FocusTerminal)
	}
	idx := 0
	for i, target := range order {
		if target == s.Focus {
			idx = i
			break
		}
	}
	if forward {
		idx = (idx + 1) % len(order)
	} else {
		idx = (idx - 1 + len(order)) % len(order)
	}
	s.Focus = order[idx]
	return s.Focus
}

// KeyAction describes how a key event is routed by first responder.
type KeyAction string

const (
	ActionForwardToHarness KeyAction = "forward_to_harness"
	ActionDetachDock       KeyAction = "detach_dock"
	ActionClearSelection   KeyAction = "clear_selection"
	ActionCycleFocus       KeyAction = "cycle_focus"
	ActionUnhandled        KeyAction = "unhandled"
)

// RouteKey determines where Escape, Cmd+W, and Tab are routed.
func (s *ShellState) RouteKey(key string, cmd, shift bool) KeyAction {
	if cmd && (key == "w" || key == "W") && s.Dock.Open {
		s.CloseDock()
		return ActionDetachDock
	}
	if key == "Escape" {
		if s.Focus == FocusTerminal && s.Dock.Open {
			return ActionForwardToHarness
		}
		s.Selection = nil
		return ActionClearSelection
	}
	if key == "Tab" {
		s.CycleFocus(!shift)
		return ActionCycleFocus
	}
	return ActionUnhandled
}

// SelectionSummary returns a concise human-readable summary of the selection.
func (s *ShellState) SelectionSummary() string {
	switch len(s.Selection) {
	case 0:
		return "No selection"
	case 1:
		n := s.Selection[0]
		return fmt.Sprintf("%s (%s) — %s", n.Name, n.Kind, n.Status)
	default:
		return fmt.Sprintf("%d nodes selected", len(s.Selection))
	}
}
