package launcher

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/desktop/canvas"
	"github.com/romanvolkov/flows/desktop/inspector"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
)

func isolatedHome(t *testing.T) paths.Home {
	t.Helper()
	t.Setenv("FLOWS_HOME", filepath.Join(t.TempDir(), "home"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.InitRuntime() })
	return home
}

func TestCrashRecoveryAfterIntentCommitAndBeforeRegistration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	home := isolatedHome(t)
	dbPath := filepath.Join(home.State, "flows.db")

	// Phase 1: Commit intent, then "kill" daemon before starting host.
	st1, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sess1 := localapi.NewSessionManager(st1, home.State)
	runs1 := localapi.NewRunManager(home, st1, sess1)

	req := localapi.CreateRunRequest{
		RequestID: "req-crash-1",
		Harness:   harness.KindClaude,
		WorkDir:   t.TempDir(),
		Command:   "/bin/sh",
		Args:      []string{"-c", "echo SLICE1; sleep 3"},
	}
	intent, err := runs1.CommitIntent(ctx, req)
	if err != nil {
		t.Fatalf("CommitIntent: %v", err)
	}
	_ = st1.Close()

	// Phase 2: Restart daemon, retry same RequestID -> must produce 1 root, 1 attempt, 1 host.
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sess2 := localapi.NewSessionManager(st2, home.State)
	runs2 := localapi.NewRunManager(home, st2, sess2)

	resp1, err := runs2.LaunchSession(ctx, req)
	if err != nil {
		t.Fatalf("LaunchSession retry 1: %v", err)
	}
	if resp1.RunID != intent.RunID || resp1.AttemptID != intent.AttemptID {
		t.Fatalf("retry created duplicate root/attempt: got %+v, want %+v", resp1, intent)
	}

	// Simulate host already started on disk before daemon registration (fresh RunManager without in-memory map).
	st3, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	sess3 := localapi.NewSessionManager(st3, home.State)
	runs3 := localapi.NewRunManager(home, st3, sess3)

	resp2, err := runs3.LaunchSession(ctx, req)
	if err != nil {
		t.Fatalf("LaunchSession retry 2: %v", err)
	}
	if resp2.SessionID != resp1.SessionID || resp2.AttemptID != resp1.AttemptID {
		t.Fatalf("expected adoption of existing session %s, got %s", resp1.SessionID, resp2.SessionID)
	}

	snap, err := st3.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Runs) != 1 || len(snap.Nodes) != 1 {
		t.Fatalf("expected 1 run and 1 node, got %d runs / %d nodes", len(snap.Runs), len(snap.Nodes))
	}

	// Clean up live host process.
	runs2.CloseHosts()
}

func TestTwoRootsLauncherInspectorAndDockReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	home := isolatedHome(t)
	st, err := store.Open(ctx, filepath.Join(home.State, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sess := localapi.NewSessionManager(st, home.State)
	runs := localapi.NewRunManager(home, st, sess)
	defer runs.CloseHosts()

	shell := inspector.NewShellState()

	l1 := OpenNew(t.TempDir(), harness.KindClaude)
	l1.Command = "/bin/sh"
	l1.Args = []string{"-c", "echo ROOT1; sleep 2"}
	r1, err := l1.Submit(ctx, runs, shell)
	if err != nil {
		t.Fatalf("Submit r1: %v", err)
	}
	if !shell.Dock.Open || shell.Focus != inspector.FocusTerminal || len(shell.Selection) != 1 {
		t.Fatalf("expected dock open and terminal focused, got %+v", shell)
	}

	l2 := OpenNew(t.TempDir(), harness.KindCodex)
	l2.Command = "/bin/sh"
	l2.Args = []string{"-c", "echo ROOT2; sleep 2"}
	r2, err := l2.Submit(ctx, runs, shell)
	if err != nil {
		t.Fatalf("Submit r2: %v", err)
	}

	// Two roots laid out stably on one canvas.
	wl := canvas.Layout(canvas.Graph{
		Nodes: []canvas.Node{
			{ID: r1.NodeID, RunID: r1.RunID, Kind: canvas.KindSession, Title: "claude", State: canvas.StateRunning},
			{ID: r2.NodeID, RunID: r2.RunID, Kind: canvas.KindSession, Title: "codex", State: canvas.StateRunning},
		},
	}, canvas.DefaultLayoutConfig(), nil)
	if len(wl.Components) != 2 {
		t.Fatalf("expected 2 components for 2 roots, got %d", len(wl.Components))
	}

	// Dock detach + reconnect.
	shell.CloseDock()
	if shell.Dock.InputOwner {
		t.Fatalf("dock close must release input ownership")
	}
	shell.OpenDock(r1.SessionID, r1.SocketPath)
	if !shell.Dock.Open || !shell.Dock.InputOwner {
		t.Fatalf("dock reconnect failed")
	}

	// Inspector: waiting label, critical state text, open chat with unresolved lease, distinct roots.
	ts, err := harness.NewTranscriptStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = ts.AppendTurn(harness.Turn{Role: "user", Text: "saved question"})

	attID := r1.AttemptID
	nodeInsp := inspector.BuildNodeInspector(store.Node{
		ID:        r1.NodeID,
		RunID:     r1.RunID,
		Kind:      "session",
		Name:      "claude",
		Status:    store.Interrupted,
		AttemptID: &attID,
		Wait:      &store.WaitReason{Kind: "edge", Ref: "build-step"},
	}, l1.WorkDir, host.LeaseUnresolved, nil, nil, "SLICE1")

	if nodeInsp.WaitLabel != "Waiting for build-step" {
		t.Fatalf("unexpected wait label: %q", nodeInsp.WaitLabel)
	}
	if nodeInsp.CriticalStateText == "" {
		t.Fatalf("expected critical state text")
	}
	if nodeInsp.CanContinueChat(store.Interrupted) {
		t.Fatalf("continue chat must be blocked when checkout lease is unresolved")
	}
	turns, _, err := nodeInsp.OpenChat(ts)
	if err != nil || len(turns) != 1 {
		t.Fatalf("OpenChat should read history even with unresolved lease: turns=%v err=%v", turns, err)
	}

	roots := inspector.DistinctRoots([]inspector.NodeSummary{
		{ID: "a", RunID: r1.RunID},
		{ID: "b", RunID: r1.RunID},
		{ID: "c", RunID: r2.RunID},
	})
	if len(roots) != 2 {
		t.Fatalf("expected 2 distinct roots, got %v", roots)
	}
}
