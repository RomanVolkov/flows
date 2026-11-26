package host

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

func TestAdoptLiveHostAcrossDaemonRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stateRoot := t.TempDir()
	hostDir := filepath.Join(stateRoot, "hosts", "sess-adopt")
	sock := testSocketPath(t)

	h, err := Start(ctx, Config{
		SessionID:  "sess-adopt",
		AttemptID:  "att-adopt",
		StateDir:   hostDir,
		SocketPath: sock,
		Command:    "/bin/sh",
		Args:       []string{"-c", "stty -echo; echo FIRST_LINE; while IFS= read -r line; do echo ECHO:$line; [ \"$line\" = \"done\" ] && break; done"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = h.RequestStop(300 * time.Millisecond)
		_ = h.Close()
	}()

	// Wait for FIRST_LINE to be produced.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Record().StreamSeq > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	seqBefore := h.Record().StreamSeq
	recBefore := h.Record()

	// Simulate daemon restart: reconcile from disk without touching the live host.
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	report, err := ReconcileAll(ctx, st, filepath.Join(stateRoot, "hosts"), stateRoot)
	if err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	if len(report.Adopted) != 1 {
		t.Fatalf("expected 1 adopted host, got %d", len(report.Adopted))
	}
	adopted := report.Adopted[0]
	if adopted.StartNonce != recBefore.StartNonce || adopted.Child.PID != recBefore.Child.PID || adopted.Child.BirthNano != recBefore.Child.BirthNano {
		t.Fatalf("adopted record mismatch: got %+v, want %+v", adopted, recBefore)
	}

	// Reattach to adopted host and verify same output sequence + live interaction.
	c, err := DialAttach(ctx, adopted.SocketPath, adopted.SessionID, AttachOptions{Nonce: adopted.StartNonce})
	if err != nil {
		t.Fatalf("DialAttach: %v", err)
	}
	defer c.Close()

	var buf bytes.Buffer
	for {
		f, err := c.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if f.Kind == FrameOutput {
			buf.Write(f.Payload)
		} else if f.Kind == FrameInputOwner {
			break
		}
	}
	if !strings.Contains(buf.String(), "FIRST_LINE") {
		t.Fatalf("expected FIRST_LINE in replay, got %q", buf.String())
	}

	if err := c.WriteInput(ctx, []byte("done\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	for {
		f, err := c.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame exit: %v", err)
		}
		if f.Kind == FrameOutput {
			buf.Write(f.Payload)
		}
		if f.Kind == FrameExit {
			break
		}
	}
	if !strings.Contains(buf.String(), "ECHO:done") {
		t.Fatalf("expected ECHO:done, got %q", buf.String())
	}
	if h.Record().StreamSeq <= seqBefore {
		t.Fatalf("expected stream seq to advance past %d, got %d", seqBefore, h.Record().StreamSeq)
	}
}

func TestInspectAbsencePIDReuseAndLostHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Absence.
	missing, err := InspectHost(filepath.Join(t.TempDir(), "missing"), "")
	if err != nil {
		t.Fatalf("InspectHost missing: %v", err)
	}
	if missing.Health != HealthMissing || missing.Lease != LeaseReleased {
		t.Fatalf("expected missing/released, got %s/%s", missing.Health, missing.Lease)
	}

	// 2. PID reuse (birth time mismatch) keeps lease unresolved.
	reuseDir := t.TempDir()
	selfProc, err := ProbeProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	reuseRec := Record{
		HostID:     "h-reuse",
		SessionID:  "s-reuse",
		AttemptID:  "a-reuse",
		StartNonce: "nonce-reuse",
		Host:       ProcessIdentity{PID: selfProc.PID, PGID: selfProc.PGID, BirthNano: selfProc.BirthNano - 1000},
		Child:      ProcessIdentity{PID: selfProc.PID, PGID: selfProc.PGID, BirthNano: selfProc.BirthNano - 1000},
		Status:     "running",
	}
	raw, _ := json.Marshal(reuseRec)
	if err := writeFileAtomic(filepath.Join(reuseDir, "host.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	inspReuse, err := InspectHost(reuseDir, "")
	if err != nil {
		t.Fatalf("InspectHost reuse: %v", err)
	}
	if inspReuse.Health != HealthLost || inspReuse.Lease != LeaseUnresolved {
		t.Fatalf("expected lost/unresolved on PID reuse, got %s/%s (%s)", inspReuse.Health, inspReuse.Lease, inspReuse.LeaseReason)
	}

	// 3. Lost host with live child keeps lease unresolved and marks node interrupted.
	cmd := exec.Command("/bin/sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	childProc, err := ProbeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runID, err := st.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.AddNode(ctx, store.NewNode{RunID: runID, Kind: "session", Name: "lost-node"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := st.StartAttempt(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}

	stateRoot := t.TempDir()
	lostDir := filepath.Join(stateRoot, "hosts", "s-lost")
	if err := os.MkdirAll(lostDir, 0700); err != nil {
		t.Fatal(err)
	}
	lostRec := Record{
		HostID:     "h-lost",
		SessionID:  "s-lost",
		AttemptID:  att.ID,
		StartNonce: "nonce-lost",
		Host:       ProcessIdentity{PID: 999999, PGID: 999999, BirthNano: 12345},
		Child:      childProc,
		Status:     "running",
	}
	lostRaw, _ := json.Marshal(lostRec)
	if err := writeFileAtomic(filepath.Join(lostDir, "host.json"), lostRaw, 0600); err != nil {
		t.Fatal(err)
	}

	report, err := ReconcileAll(ctx, st, filepath.Join(stateRoot, "hosts"), stateRoot)
	if err != nil {
		t.Fatalf("ReconcileAll lost: %v", err)
	}
	if len(report.Unresolved) != 1 || report.Unresolved[0] != att.ID {
		t.Fatalf("expected unresolved attempt %s, got %v", att.ID, report.Unresolved)
	}
	if len(report.Interrupted) != 1 || report.Interrupted[0] != att.ID {
		t.Fatalf("expected interrupted attempt %s, got %v", att.ID, report.Interrupted)
	}
	updated, err := st.Node(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.Interrupted {
		t.Fatalf("node status = %s, want interrupted", updated.Status)
	}
}

func TestRecoverOfflineOutboxCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runID, err := st.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.AddNode(ctx, store.NewNode{RunID: runID, Kind: "session", Name: "offline-node"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := st.StartAttempt(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}

	stateRoot := t.TempDir()
	ob, err := NewOutbox(stateRoot, att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ob.Capture("evt-offline-1", "succeeded", []byte(`{"result":"ok"}`), nil); err != nil {
		t.Fatal(err)
	}

	// Both host and harness exited while daemon was offline; ReconcileAll recovers outbox first.
	report, err := ReconcileAll(ctx, st, filepath.Join(stateRoot, "hosts"), stateRoot)
	if err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	if len(report.Completed) != 1 || report.Completed[0] != att.ID {
		t.Fatalf("expected completed attempt %s, got %v", att.ID, report.Completed)
	}

	updated, err := st.Node(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.Succeeded {
		t.Fatalf("node status = %s, want succeeded", updated.Status)
	}
	pending, err := ob.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("expected 0 pending outbox records, got %d (err=%v)", len(pending), err)
	}
}
