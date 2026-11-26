package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

func testSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fh-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

func TestPersistIdentityAndVerifyProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stateDir := t.TempDir()
	workDir := t.TempDir()
	sock := testSocketPath(t)

	h, err := Start(ctx, Config{
		SessionID:  "sess-1",
		AttemptID:  "att-1",
		StateDir:   stateDir,
		SocketPath: sock,
		WorkDir:    workDir,
		Command:    "/bin/sh",
		Args:       []string{"-c", "pwd; sleep 2"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Close()

	if err := h.WaitCheckoutReady(ctx); err != nil {
		t.Fatalf("WaitCheckoutReady: %v", err)
	}

	rec, err := LoadRecord(stateDir)
	if err != nil {
		t.Fatalf("LoadRecord: %v", err)
	}
	if rec.HostID == "" || rec.StartNonce == "" {
		t.Fatalf("expected non-empty host_id and start_nonce, got %+v", rec)
	}
	if rec.Child.PID <= 0 || rec.Child.PGID <= 0 || rec.Child.BirthNano <= 0 {
		t.Fatalf("expected persisted child identity, got %+v", rec.Child)
	}
	if rec.WorkDir != workDir {
		t.Fatalf("workdir = %q, want %q", rec.WorkDir, workDir)
	}
	if err := VerifyProcess(rec.Child); err != nil {
		t.Fatalf("VerifyProcess(child): %v", err)
	}

	// Mutated birth time must fail verification (PID reuse fence).
	bad := rec.Child
	bad.BirthNano++
	if err := VerifyProcess(bad); !errors.Is(err, ErrProcessMismatch) {
		t.Fatalf("expected ErrProcessMismatch, got %v", err)
	}

	if err := h.RequestStop(500 * time.Millisecond); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	final := h.Wait()
	if final.Status != "exited" {
		t.Fatalf("status = %q, want exited", final.Status)
	}
}

func TestRejectUnsupportedAttachMethod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sock := testSocketPath(t)
	h, err := Start(ctx, Config{
		SessionID:  "sess-method",
		AttemptID:  "att-method",
		StateDir:   t.TempDir(),
		SocketPath: sock,
		Command:    "/bin/sh",
		Args:       []string{"-c", "sleep 2"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = h.RequestStop(200 * time.Millisecond)
		_ = h.Close()
	}()

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequestWithContext(ctx, method, "http://flows.local/v1/sessions/sess-method/attach", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("%s request: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodGet {
			t.Fatalf("Allow = %q, want GET", got)
		}
	}
}

func TestInitialLateAttachTwoObserversAndResize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sock := testSocketPath(t)
	h, err := Start(ctx, Config{
		SessionID:     "sess-obs",
		AttemptID:     "att-obs",
		StateDir:      t.TempDir(),
		SocketPath:    sock,
		Command:       "/bin/sh",
		Args:          []string{"-c", "stty -echo; echo READY; while IFS= read -r line; do echo GOT:$line; [ \"$line\" = \"quit\" ] && break; done"},
		InitialPrompt: "hello-initial",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Close()

	// Wait briefly for initial output to enter scrollback before first attach (late attach).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Record().StreamSeq > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	c1, err := DialAttach(ctx, sock, "sess-obs", AttachOptions{ClientID: "c1"})
	if err != nil {
		t.Fatalf("DialAttach c1: %v", err)
	}
	defer c1.Close()

	var c1Buf bytes.Buffer
	var c1Owner InputOwnerEvent
	for {
		f, err := c1.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("c1 ReadFrame: %v", err)
		}
		if f.Kind == FrameOutput {
			c1Buf.Write(f.Payload)
		} else if f.Kind == FrameInputOwner {
			_ = json.Unmarshal(f.Payload, &c1Owner)
			break
		}
	}
	if c1Owner.OwnerID != "c1" {
		t.Fatalf("expected c1 as input owner, got %+v", c1Owner)
	}

	// Second observer attaches; must not steal input ownership.
	c2, err := DialAttach(ctx, sock, "sess-obs", AttachOptions{ClientID: "c2"})
	if err != nil {
		t.Fatalf("DialAttach c2: %v", err)
	}
	defer c2.Close()

	var c2Owner InputOwnerEvent
	for {
		f, err := c2.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("c2 ReadFrame: %v", err)
		}
		if f.Kind == FrameInputOwner {
			_ = json.Unmarshal(f.Payload, &c2Owner)
			break
		}
	}
	if c2Owner.OwnerID != "c1" {
		t.Fatalf("expected c1 to remain owner for c2, got %+v", c2Owner)
	}

	// Non-owner c2 sending input must be fenced.
	if err := c2.WriteInput(ctx, []byte("from-c2\n")); err != nil {
		t.Fatalf("c2 WriteInput: %v", err)
	}
	var fenced bool
	for range 5 {
		f, err := c2.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("c2 ReadFrame fence: %v", err)
		}
		if f.Kind == FrameControl {
			var ctrl ControlMessage
			if json.Unmarshal(f.Payload, &ctrl) == nil && ctrl.Type == "fenced" && ctrl.Reason == "not_input_owner" {
				fenced = true
				break
			}
		}
	}
	if !fenced {
		t.Fatalf("expected c2 input to be fenced")
	}

	// Stale epoch client must be fenced immediately.
	if stale, err := DialAttach(ctx, sock, "sess-obs", AttachOptions{ClientID: "stale", Epoch: 0}); err == nil {
		stale.Close()
	}
	if staleEpoch, err := DialAttach(ctx, sock, "sess-obs", AttachOptions{ClientID: "stale-nonce", Nonce: "wrong-nonce"}); err == nil {
		f, err := staleEpoch.ReadFrame(ctx)
		staleEpoch.Close()
		if err != nil || f.Kind != FrameControl {
			t.Fatalf("expected fenced control frame for stale nonce, got err=%v frame=%+v", err, f)
		}
	}

	// Owner c1 resizes PTY and sends quit.
	if err := c1.SendResize(ctx, 110, 35, 0, 0); err != nil {
		t.Fatalf("c1 SendResize: %v", err)
	}
	if err := c1.WriteInput(ctx, []byte("quit\n")); err != nil {
		t.Fatalf("c1 WriteInput: %v", err)
	}

	// Both c1 and c2 should observe exit.
	waitExit := func(c *AttachConn, name string) {
		for {
			f, err := c.ReadFrame(ctx)
			if err != nil {
				t.Fatalf("%s ReadFrame waiting exit: %v", name, err)
			}
			if f.Kind == FrameExit {
				return
			}
		}
	}
	waitExit(c1, "c1")
	waitExit(c2, "c2")

	rec := h.Wait()
	if rec.Cols != 110 || rec.Rows != 35 {
		t.Fatalf("persisted dimensions = %dx%d, want 110x35", rec.Cols, rec.Rows)
	}
	if !strings.Contains(c1Buf.String(), "GOT:hello-initial") && !strings.Contains(c1Buf.String(), "READY") {
		t.Fatalf("expected replayed output in c1, got %q", c1Buf.String())
	}
}

func TestAttachTTYFullScreenAndExitBeforeAttach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Part 1: Full-screen alt-screen program via AttachTTY over a real PTY pair.
	sock1 := testSocketPath(t)
	h1, err := Start(ctx, Config{
		SessionID:  "sess-tty",
		AttemptID:  "att-tty",
		StateDir:   t.TempDir(),
		SocketPath: sock1,
		Command:    "/bin/sh",
		Args:       []string{"-c", "printf '\\033[?1049hFULLSCREEN\\033[?1049l'; exit 0"},
	})
	if err != nil {
		t.Fatalf("Start h1: %v", err)
	}
	defer h1.Close()

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	_ = pty.Setsize(master, &pty.Winsize{Cols: 90, Rows: 30})

	var ttyOut bytes.Buffer
	var outMu sync.Mutex
	gotScreen := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				outMu.Lock()
				ttyOut.Write(buf[:n])
				has := strings.Contains(ttyOut.String(), "FULLSCREEN")
				outMu.Unlock()
				if has {
					select {
					case <-gotScreen:
					default:
						close(gotScreen)
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	if err := AttachTTY(ctx, sock1, "sess-tty", slave, slave); err != nil {
		t.Fatalf("AttachTTY: %v", err)
	}
	select {
	case <-gotScreen:
	case <-time.After(2 * time.Second):
		outMu.Lock()
		t.Fatalf("expected FULLSCREEN in AttachTTY output, got %q", ttyOut.String())
		outMu.Unlock()
	}

	// Part 2: Terminal exits BEFORE first attachment.
	sock2 := testSocketPath(t)
	stateDir2 := t.TempDir()
	h2, err := Start(ctx, Config{
		SessionID:  "sess-exited",
		AttemptID:  "att-exited",
		StateDir:   stateDir2,
		SocketPath: sock2,
		Command:    "/bin/sh",
		Args:       []string{"-c", "echo EARLY_EXIT; exit 7"},
	})
	if err != nil {
		t.Fatalf("Start h2: %v", err)
	}
	defer h2.Close()

	rec2 := h2.Wait()
	if rec2.ExitCode == nil || *rec2.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %+v", rec2.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(stateDir2, "exit.json")); err != nil {
		t.Fatalf("expected exit.json persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir2, "snapshot.bin")); err != nil {
		t.Fatalf("expected snapshot.bin persisted: %v", err)
	}

	c, err := DialAttach(ctx, sock2, "sess-exited", AttachOptions{})
	if err != nil {
		t.Fatalf("DialAttach after exit: %v", err)
	}
	defer c.Close()

	var replayed bytes.Buffer
	var gotExit *ExitEvent
	for {
		f, err := c.ReadFrame(ctx)
		if err != nil {
			break
		}
		switch f.Kind {
		case FrameOutput:
			replayed.Write(f.Payload)
		case FrameExit:
			var evt ExitEvent
			if err := json.Unmarshal(f.Payload, &evt); err == nil {
				gotExit = &evt
			}
		}
		if gotExit != nil {
			break
		}
	}
	if gotExit == nil || gotExit.ExitCode != 7 {
		t.Fatalf("expected FrameExit with code 7, got %+v", gotExit)
	}
	if !strings.Contains(replayed.String(), "EARLY_EXIT") {
		t.Fatalf("expected EARLY_EXIT in late replay, got %q", replayed.String())
	}
}

func TestCompletionOutboxPersistenceAndErrors(t *testing.T) {
	stateDir := t.TempDir()
	ob, err := NewOutbox(stateDir, "att-outbox")
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}

	receipt, err := ob.Capture("evt-1", "succeeded", []byte(`{"ok":true}`), []ArtifactInput{
		{Name: "summary.txt", Kind: "summary", Data: []byte("all green")},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if receipt.EventID != "evt-1" || receipt.Digest == "" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	// Re-open outbox from disk (simulating host exit + restart) and verify pending record + artifact.
	ob2, err := NewOutbox(stateDir, "att-outbox")
	if err != nil {
		t.Fatalf("NewOutbox reopen: %v", err)
	}
	pending, err := ob2.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].EventID != "evt-1" || len(pending[0].Artifacts) != 1 {
		t.Fatalf("unexpected pending records: %+v", pending)
	}
	artData, err := os.ReadFile(filepath.Join(stateDir, "attempts", "att-outbox", pending[0].Artifacts[0].RelativePath))
	if err != nil || string(artData) != "all green" {
		t.Fatalf("artifact read = %q, err=%v", string(artData), err)
	}

	if err := ob2.MarkDelivered("evt-1"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	pendingAfter, err := ob2.Pending()
	if err != nil || len(pendingAfter) != 0 {
		t.Fatalf("expected 0 pending after delivery, got %d (err=%v)", len(pendingAfter), err)
	}

	// Invalid artifact path must fail with storage error and not write outbox record.
	if _, err := ob2.Capture("evt-bad", "succeeded", []byte(`{}`), []ArtifactInput{
		{Name: "../escape.txt", Kind: "bad", Data: []byte("nope")},
	}); err == nil {
		t.Fatalf("expected error for escaping artifact name")
	}
}
