package inspector

import "testing"

func TestShellFocusDockAndWindowClose(t *testing.T) {
	s := NewShellState()
	if s.ShowTerminalControls() {
		t.Fatalf("terminal controls should be hidden when no session exists")
	}

	// Empty session root is its harness node.
	root := EmptySessionRoot("run-1", "sess-1", "claude")
	if root.Kind != "session" || root.Name != "claude" || root.SessionID != "sess-1" {
		t.Fatalf("unexpected empty session root: %+v", root)
	}

	// Cycle focus without terminal dock.
	if got := s.CycleFocus(true); got != FocusInspector {
		t.Fatalf("expected inspector, got %s", got)
	}
	if got := s.CycleFocus(true); got != FocusSidebar {
		t.Fatalf("expected sidebar, got %s", got)
	}

	// Open dock claims input ownership and shows terminal controls.
	s.OpenDock("sess-1", "/tmp/sess-1.sock")
	if !s.ShowTerminalControls() || !s.Dock.InputOwner || s.Focus != FocusTerminal {
		t.Fatalf("unexpected dock state after open: %+v focus=%s", s.Dock, s.Focus)
	}

	// Escape goes to harness when terminal has focus.
	if act := s.RouteKey("Escape", false, false); act != ActionForwardToHarness {
		t.Fatalf("expected ActionForwardToHarness, got %s", act)
	}

	// Cmd+W detaches dock and releases input ownership.
	if act := s.RouteKey("w", true, false); act != ActionDetachDock {
		t.Fatalf("expected ActionDetachDock, got %s", act)
	}
	if s.Dock.Open || s.Dock.InputOwner || s.ShowTerminalControls() {
		t.Fatalf("expected dock closed and input released: %+v", s.Dock)
	}

	// Re-open dock, then close window: input ownership released, session preserved.
	s.OpenDock("sess-1", "/tmp/sess-1.sock")
	s.CloseWindow()
	if s.WindowOpen || s.Dock.InputOwner || s.Dock.SessionID != "sess-1" {
		t.Fatalf("expected window closed, input released, session preserved: %+v", s)
	}
}
