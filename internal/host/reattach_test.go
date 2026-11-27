package host

import (
	"bufio"
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
		Args:       []string{"-c", "printf '\\033[?1049hFULLSCREEN'; IFS= read -r _; printf '\\033[?1049l'; exit 0"},
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

	attachDone := make(chan error, 1)
	go func() {
		attachDone <- AttachTTY(ctx, sock1, "sess-tty", slave, slave)
	}()
	select {
	case <-gotScreen:
		_, _ = master.Write([]byte("\n"))
	case <-time.After(2 * time.Second):
		outMu.Lock()
		t.Fatalf("expected FULLSCREEN in AttachTTY output, got %q", ttyOut.String())
		outMu.Unlock()
	}
	if err := <-attachDone; err != nil {
		t.Fatalf("AttachTTY: %v", err)
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

type bufferedScreen struct {
	pending []byte
}

func (s *bufferedScreen) Feed(data []byte, _ *os.File) {
	s.pending = append(s.pending, data...)
}
func (s *bufferedScreen) Resize(pty.Winsize) error { return nil }
func (s *bufferedScreen) Replay() ([]byte, error)  { return nil, nil }
func (s *bufferedScreen) RenderFrame(bool) ([]byte, error) {
	data := s.pending
	s.pending = nil
	return data, nil
}

func TestScreenPumpCoalescesOutputForCanvasClients(t *testing.T) {
	canvas := &attachObserver{out: make(chan Frame, 4), screenOnly: true}
	raw := &attachObserver{out: make(chan Frame, 4)}
	h := &Host{
		cfg: Config{State: &bufferedScreen{}}, done: make(chan struct{}), screenWake: make(chan struct{}, 1),
		observers: map[string]*attachObserver{"canvas": canvas, "raw": raw},
	}
	defer close(h.done)

	h.publishOutput([]byte("one"))
	h.publishOutput([]byte("two"))
	for _, want := range []string{"one", "two"} {
		if f := <-raw.out; f.Kind != FrameOutput || string(f.Payload) != want {
			t.Fatalf("raw client received %+v; want output %q", f, want)
		}
	}
	go h.pumpScreen()
	for _, observer := range []*attachObserver{canvas, raw} {
		select {
		case f := <-observer.out:
			if f.Kind != FrameScreen || f.Seq != 6 || string(f.Payload) != "onetwo" {
				t.Fatalf("received %+v; want a screen frame containing both writes", f)
			}
		case <-time.After(time.Second):
			t.Fatal("screen pump failed to flush pending output")
		}
	}
}

type fakeSnapshotState struct {
	mu           sync.Mutex
	buf          bytes.Buffer
	scrolled     int
	snapshotData []byte
	snapshotErr  error
	replayErr    error
}

func (s *fakeSnapshotState) Feed(data []byte, _ *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Write(data)
}

func (s *fakeSnapshotState) Resize(pty.Winsize) error { return nil }

func (s *fakeSnapshotState) Replay() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.replayErr != nil {
		return nil, s.replayErr
	}
	return append([]byte(nil), s.buf.Bytes()...), nil
}

func (s *fakeSnapshotState) SnapshotFrame() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.snapshotData...), s.snapshotErr
}

func (s *fakeSnapshotState) ScrolledRows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrolled
}

func (s *fakeSnapshotState) SetScrolledRows(rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rows < 0 {
		rows = 0
	}
	s.scrolled = rows
	return nil
}

func TestRawAndFallbackReplayWithScrolledAndMalformedSnapshots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	state := &fakeSnapshotState{
		scrolled:     17,
		snapshotData: []byte(`{"full":true,"cols":80,"rows":24,"fg":1,"bg":0,"cursor":{"x":0,"y":0,"visible":true,"style":"block"},"lines":[]}`),
	}
	sock := testSocketPath(t)
	h, err := Start(ctx, Config{
		SessionID:  "sess-replay-modes",
		AttemptID:  "att-replay-modes",
		StateDir:   t.TempDir(),
		SocketPath: sock,
		Command:    "/bin/sh",
		Args:       []string{"-c", "echo REPLAY_LINE; sleep 2"},
		State:      state,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = h.RequestStop(200 * time.Millisecond)
		_ = h.Close()
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Record().StreamSeq > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 1. Raw attach (ScreenOnly=false) must receive FrameOutput and scrolled Rows in replay_end, NOT FrameScreen.
	rawClient, err := DialAttach(ctx, sock, "sess-replay-modes", AttachOptions{ClientID: "raw-1"})
	if err != nil {
		t.Fatalf("DialAttach raw: %v", err)
	}
	var gotOutput bytes.Buffer
	var gotReplayEnd ControlMessage
	for {
		f, err := rawClient.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("raw ReadFrame: %v", err)
		}
		if f.Kind == FrameScreen {
			t.Fatalf("raw attach unexpectedly received FrameScreen")
		}
		if f.Kind == FrameOutput {
			gotOutput.Write(f.Payload)
		}
		if f.Kind == FrameControl {
			var ctrl ControlMessage
			if json.Unmarshal(f.Payload, &ctrl) == nil && ctrl.Type == "replay_end" {
				gotReplayEnd = ctrl
				break
			}
		}
	}
	rawClient.Close()
	if !strings.Contains(gotOutput.String(), "REPLAY_LINE") {
		t.Fatalf("raw replay missing REPLAY_LINE, got %q", gotOutput.String())
	}
	if gotReplayEnd.Rows != 17 {
		t.Fatalf("replay_end rows = %d, want 17", gotReplayEnd.Rows)
	}

	// 2. ScreenOnly attach with malformed snapshot JSON must fall back to FrameOutput replay.
	state.mu.Lock()
	state.snapshotData = []byte(`{"full":true,`)
	state.mu.Unlock()
	malformedClient, err := DialAttach(ctx, sock, "sess-replay-modes", AttachOptions{ClientID: "screen-bad", ScreenOnly: true})
	if err != nil {
		t.Fatalf("DialAttach malformed screen: %v", err)
	}
	gotOutput.Reset()
	for {
		f, err := malformedClient.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("malformed ReadFrame: %v", err)
		}
		if f.Kind == FrameScreen {
			t.Fatalf("malformed snapshot must not be sent as FrameScreen")
		}
		if f.Kind == FrameOutput {
			gotOutput.Write(f.Payload)
		}
		if f.Kind == FrameControl {
			var ctrl ControlMessage
			if json.Unmarshal(f.Payload, &ctrl) == nil && ctrl.Type == "replay_end" {
				break
			}
		}
	}
	malformedClient.Close()
	if !strings.Contains(gotOutput.String(), "REPLAY_LINE") {
		t.Fatalf("fallback replay after malformed snapshot missing REPLAY_LINE, got %q", gotOutput.String())
	}

	// 3. ScreenOnly attach with unavailable snapshot and unavailable Replay() must fall back to h.scrollback.
	state.mu.Lock()
	state.snapshotData = nil
	state.snapshotErr = errors.New("snapshot unavailable")
	state.replayErr = errors.New("replay unavailable")
	state.mu.Unlock()
	fallbackClient, err := DialAttach(ctx, sock, "sess-replay-modes", AttachOptions{ClientID: "screen-err", ScreenOnly: true})
	if err != nil {
		t.Fatalf("DialAttach unavailable snapshot: %v", err)
	}
	defer fallbackClient.Close()
	gotOutput.Reset()
	for {
		f, err := fallbackClient.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("fallback ReadFrame: %v", err)
		}
		if f.Kind == FrameOutput {
			gotOutput.Write(f.Payload)
		}
		if f.Kind == FrameControl {
			var ctrl ControlMessage
			if json.Unmarshal(f.Payload, &ctrl) == nil && ctrl.Type == "replay_end" {
				break
			}
		}
	}
	if !strings.Contains(gotOutput.String(), "REPLAY_LINE") {
		t.Fatalf("fallback replay from scrollback missing REPLAY_LINE, got %q", gotOutput.String())
	}
}

func TestAttachNativeOwnershipScrollSyncAndDetach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	state := &fakeSnapshotState{scrolled: 9}
	sock := testSocketPath(t)
	h, err := Start(ctx, Config{
		SessionID:  "sess-native",
		AttemptID:  "att-native",
		StateDir:   t.TempDir(),
		SocketPath: sock,
		Command:    "/bin/sh",
		Args:       []string{"-c", "stty -echo; echo NATIVE_READY; while IFS= read -r line; do echo SAW:$line; [ \"$line\" = \"bye\" ] && break; done"},
		State:      state,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = h.RequestStop(200 * time.Millisecond)
		_ = h.Close()
	}()

	// Hold input ownership with c1 so native attach starts as non-owner.
	c1, err := DialAttach(ctx, sock, "sess-native", AttachOptions{ClientID: "owner-1"})
	if err != nil {
		t.Fatalf("DialAttach owner-1: %v", err)
	}
	for {
		f, err := c1.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("c1 ReadFrame: %v", err)
		}
		if f.Kind == FrameInputOwner {
			break
		}
	}

	ctlSock := testSocketPath(t)
	ctlListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: ctlSock, Net: "unix"})
	if err != nil {
		t.Fatalf("listen ctl: %v", err)
	}
	defer ctlListener.Close()

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	_ = pty.Setsize(master, &pty.Winsize{Cols: 96, Rows: 32})

	var ptyBuf bytes.Buffer
	var ptyMu sync.Mutex
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if bytes.Contains(chunk, []byte("\x1b[5n")) {
					_, _ = master.Write([]byte("\x1b[0n"))
				}
				ptyMu.Lock()
				ptyBuf.Write(chunk)
				ptyMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	nativeErr := make(chan error, 1)
	go func() {
		nativeErr <- AttachNative(ctx, sock, "sess-native", slave, slave, NativeAttachOptions{
			ClientID:      "native-2",
			ControlSocket: ctlSock,
		})
	}()

	ctlConn, err := ctlListener.Accept()
	if err != nil {
		t.Fatalf("accept ctl: %v", err)
	}
	defer ctlConn.Close()

	events := make(chan nativeControlEvent, 16)
	go func() {
		sc := bufio.NewScanner(ctlConn)
		for sc.Scan() {
			var evt nativeControlEvent
			if json.Unmarshal(sc.Bytes(), &evt) == nil {
				events <- evt
			}
		}
	}()

	var gotReady, gotNonOwner bool
	deadline := time.After(3 * time.Second)
	for !(gotReady && gotNonOwner) {
		select {
		case evt := <-events:
			if evt.Type == "ready" {
				if evt.Scrolled != 9 {
					t.Fatalf("ready scrolled = %d, want 9", evt.Scrolled)
				}
				gotReady = true
			} else if evt.Type == "input_owner" && !evt.IsOwner && evt.OwnerID == "owner-1" {
				gotNonOwner = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for ready and initial input_owner (ready=%v nonOwner=%v)", gotReady, gotNonOwner)
		}
	}

	// Non-owner input written into native PTY must NOT reach host child.
	_, _ = master.Write([]byte("blocked\n"))
	time.Sleep(150 * time.Millisecond)
	ptyMu.Lock()
	if strings.Contains(ptyBuf.String(), "SAW:blocked") {
		ptyMu.Unlock()
		t.Fatalf("non-owner native input reached child PTY")
	}
	ptyMu.Unlock()

	// Release owner-1, then claim input from native control channel.
	c1.Close()
	_, _ = ctlConn.Write([]byte("{\"type\":\"claim_input\"}\n"))
	_, _ = ctlConn.Write([]byte("{\"type\":\"scroll_offset\",\"rows\":4}\n"))

	var becameOwner bool
	deadline = time.After(3 * time.Second)
	for !becameOwner {
		select {
		case evt := <-events:
			if evt.Type == "input_owner" && evt.IsOwner && evt.OwnerID == "native-2" {
				becameOwner = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for native-2 to become input owner")
		}
	}

	// Now input must reach child PTY and resize must have updated host dimensions.
	_, _ = master.Write([]byte("bye\n"))
	var gotExit bool
	deadline = time.After(3 * time.Second)
	for !gotExit {
		select {
		case evt := <-events:
			if evt.Type == "exit" {
				gotExit = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for exit event after owner input")
		}
	}
	if err := <-nativeErr; err != nil {
		t.Fatalf("AttachNative returned error: %v", err)
	}
	rec := h.Wait()
	if rec.Cols != 96 || rec.Rows != 32 {
		t.Fatalf("host size after owner claim = %dx%d, want 96x32", rec.Cols, rec.Rows)
	}
	if got := state.ScrolledRows(); got != 4 {
		t.Fatalf("host ScrolledRows = %d, want 4", got)
	}
	ptyMu.Lock()
	out := ptyBuf.String()
	ptyMu.Unlock()
	if !strings.Contains(out, "SAW:bye") {
		t.Fatalf("expected SAW:bye in native PTY output, got %q", out)
	}
}
