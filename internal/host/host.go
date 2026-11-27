// Package host manages persistent PTY session hosts, terminal attachment streams,
// process birth identity verification, and durable per-attempt completion outboxes.
package host

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"context"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/romanvolkov/flows/internal/terminal"
)

const (
	defaultCols    = 80
	defaultRows    = 24
	maxScrollback  = 256 * 1024
	screenInterval = 16 * time.Millisecond
)

var (
	ErrProcessMismatch  = errors.New("process identity mismatch or process exited")
	ErrCheckoutNotReady = errors.New("host identity not yet persisted")
)

// ProcessIdentity captures a process PID, process group, and kernel birth time
// so PID reuse can be detected before cancellation or adoption.
type ProcessIdentity struct {
	PID       int   `json:"pid"`
	PGID      int   `json:"pgid"`
	BirthNano int64 `json:"birth_nano"`
}

// ProbeProcess queries the OS for the current process group and birth time of pid.
func ProbeProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("invalid pid %d", pid)
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("probe process %d: %w", pid, err)
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("getpgid %d: %w", pid, err)
	}
	birth := int64(kp.Proc.P_starttime.Sec)*1e9 + int64(kp.Proc.P_starttime.Usec)*1e3
	return ProcessIdentity{
		PID:       pid,
		PGID:      pgid,
		BirthNano: birth,
	}, nil
}

// VerifyProcess confirms that expected still refers to the same live process.
func VerifyProcess(expected ProcessIdentity) error {
	if expected.PID <= 0 || expected.BirthNano <= 0 {
		return ErrProcessMismatch
	}
	actual, err := ProbeProcess(expected.PID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProcessMismatch, err)
	}
	if actual.BirthNano != expected.BirthNano {
		return fmt.Errorf("%w: birth time changed (%d != %d)", ErrProcessMismatch, actual.BirthNano, expected.BirthNano)
	}
	if expected.PGID > 0 && actual.PGID != expected.PGID {
		return fmt.Errorf("%w: pgid changed (%d != %d)", ErrProcessMismatch, actual.PGID, expected.PGID)
	}
	return nil
}

// Record is the durable on-disk state of a PTY session host.
type Record struct {
	Harness    string          `json:"harness,omitempty"`
	HostID     string          `json:"host_id"`
	SessionID  string          `json:"session_id"`
	AttemptID  string          `json:"attempt_id"`
	SocketPath string          `json:"socket_path"`
	StartNonce string          `json:"start_nonce"`
	Host       ProcessIdentity `json:"host"`
	Child      ProcessIdentity `json:"child"`
	WorkDir    string          `json:"work_dir"`
	Command    string          `json:"command"`
	Args       []string        `json:"args,omitempty"`
	Cols       uint16          `json:"cols"`
	Rows       uint16          `json:"rows"`
	StreamSeq  uint64          `json:"stream_seq"`
	Status     string          `json:"status"`
	ExitCode   *int            `json:"exit_code,omitempty"`
	ExitSignal string          `json:"exit_signal,omitempty"`
	StartedAt  int64           `json:"started_at"`
	ExitedAt   int64           `json:"exited_at,omitempty"`
}

// ExitReceipt is persisted before host shutdown when the child process terminates.
type ExitReceipt struct {
	SessionID  string          `json:"session_id"`
	AttemptID  string          `json:"attempt_id"`
	StartNonce string          `json:"start_nonce"`
	Child      ProcessIdentity `json:"child"`
	ExitCode   int             `json:"exit_code"`
	ExitSignal string          `json:"exit_signal,omitempty"`
	StreamSeq  uint64          `json:"stream_seq"`
	ExitedAt   int64           `json:"exited_at"`
}

// Config configures a PTY session host.
type Config struct {
	Harness       string
	SessionID     string
	AttemptID     string
	StateDir      string
	SocketPath    string
	WorkDir       string
	Command       string
	Args          []string
	Env           []string
	InitialPrompt string
	InitialScreen []byte
	Cols          uint16
	Rows          uint16
	State         terminal.TerminalState
}

// Host owns a child PTY process, its WebSocket attach endpoint, bounded scrollback,
// and durable state/exit receipts.
type Host struct {
	cfg           Config
	stateDir      string
	recordPath    string
	snapshotPath  string
	screenPath    string
	screenSaved   time.Time
	screenWake    chan struct{}
	exitPath      string
	pty           *os.File
	cmd           *exec.Cmd
	listener      *net.UnixListener
	server        *http.Server
	checkoutReady chan struct{}

	mu         sync.Mutex
	record     Record
	scrollback []byte
	observers  map[string]*attachObserver
	ownerID    string
	ownerEpoch uint64
	finished   bool
	done       chan struct{}
}

// Start launches the child process in a new PTY, persists host and child identity
// before allowing checkout writes, and serves GET /v1/sessions/{id}/attach over
// the configured Unix socket.
func Start(ctx context.Context, cfg Config) (*Host, error) {
	if cfg.SessionID == "" || cfg.AttemptID == "" {
		return nil, fmt.Errorf("session_id and attempt_id are required")
	}
	if cfg.StateDir == "" || cfg.SocketPath == "" || cfg.Command == "" {
		return nil, fmt.Errorf("state_dir, socket_path, and command are required")
	}
	if cfg.Cols == 0 {
		cfg.Cols = defaultCols
	}
	if cfg.Rows == 0 {
		cfg.Rows = defaultRows
	}
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	_ = os.Remove(cfg.SocketPath)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.SocketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen host socket: %w", err)
	}
	if err := os.Chmod(cfg.SocketPath, 0600); err != nil {
		listener.Close()
		return nil, err
	}

	hostID, err := randomHex(12)
	if err != nil {
		listener.Close()
		return nil, err
	}
	nonce, err := randomHex(16)
	if err != nil {
		listener.Close()
		return nil, err
	}
	hostProc, err := ProbeProcess(os.Getpid())
	if err != nil {
		listener.Close()
		return nil, err
	}

	var release func()
	if cfg.State == nil {
		cfg.State, release, err = newScreen()
		if err != nil {
			listener.Close()
			return nil, err
		}
		defer func() {
			if release != nil {
				release()
			}
		}()
	}
	winsize := pty.Winsize{Rows: cfg.Rows, Cols: cfg.Cols}
	if cfg.State != nil {
		if err := cfg.State.Resize(winsize); err != nil {
			listener.Close()
			return nil, err
		}
		if len(cfg.InitialScreen) > 0 {
			cfg.State.Feed(cfg.InitialScreen, nil)
		}
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	env := cfg.Env
	if len(env) == 0 {
		env = os.Environ()
	}
	cmd.Env = setEnv(setEnv(env, "TERM", "xterm-256color"), "COLORTERM", "truecolor")

	master, err := pty.StartWithSize(cmd, &winsize)
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("start pty child: %w", err)
	}

	childProc, err := ProbeProcess(cmd.Process.Pid)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		master.Close()
		listener.Close()
		return nil, err
	}

	h := &Host{
		cfg:           cfg,
		stateDir:      cfg.StateDir,
		recordPath:    filepath.Join(cfg.StateDir, "host.json"),
		snapshotPath:  filepath.Join(cfg.StateDir, "snapshot.bin"),
		screenPath:    filepath.Join(cfg.StateDir, "screen.json"),
		exitPath:      filepath.Join(cfg.StateDir, "exit.json"),
		pty:           master,
		cmd:           cmd,
		listener:      listener,
		checkoutReady: make(chan struct{}),
		observers:     make(map[string]*attachObserver),
		done:          make(chan struct{}),
		record: Record{
			HostID:     hostID,
			SessionID:  cfg.SessionID,
			AttemptID:  cfg.AttemptID,
			Harness:    cfg.Harness,
			SocketPath: cfg.SocketPath,
			StartNonce: nonce,
			Host:       hostProc,
			Child:      childProc,
			WorkDir:    cfg.WorkDir,
			Command:    cfg.Command,
			Args:       append([]string(nil), cfg.Args...),
			Cols:       cfg.Cols,
			Rows:       cfg.Rows,
			Status:     "running",
			StartedAt:  time.Now().UnixNano(),
		},
	}

	if _, ok := cfg.State.(interface{ RenderFrame(bool) ([]byte, error) }); ok {
		h.screenWake = make(chan struct{}, 1)
	}

	// Persist host and child process group + birth identity before allowing
	// checkout writes or delivering the initial prompt.
	if err := h.persistRecordLocked(); err != nil {
		_ = syscall.Kill(-childProc.PGID, syscall.SIGKILL)
		master.Close()
		listener.Close()
		return nil, fmt.Errorf("persist host identity: %w", err)
	}
	close(h.checkoutReady)

	h.server = &http.Server{
		Handler:           h.handler(),
		ReadHeaderTimeout: 3 * time.Second,
	}
	go func() {
		_ = h.server.Serve(listener)
	}()

	cleanup := release
	release = nil
	go func() {
		h.run(ctx)
		if cleanup != nil {
			cleanup()
		}
	}()

	if cfg.InitialPrompt != "" {
		if err := h.DeliverPrompt(cfg.InitialPrompt); err != nil {
			_ = h.RequestStop(500 * time.Millisecond)
			return nil, err
		}
	}

	return h, nil
}

func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok || name != key {
			out = append(out, item)
		}
	}
	return append(out, key+"="+value)
}

// WaitCheckoutReady blocks until host and child identities are durably persisted.
func (h *Host) WaitCheckoutReady(ctx context.Context) error {
	select {
	case <-h.checkoutReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Record returns a copy of the current host record.
func (h *Host) Record() Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := h.record
	cp.Args = append([]string(nil), h.record.Args...)
	return cp
}

// DeliverPrompt writes a resolved prompt to the child PTY after identity persistence.
func (h *Host) DeliverPrompt(prompt string) error {
	select {
	case <-h.checkoutReady:
	default:
		return ErrCheckoutNotReady
	}
	if prompt == "" {
		return nil
	}
	if !strings.HasSuffix(prompt, "\n") {
		prompt += "\n"
	}
	_, err := io.WriteString(h.pty, prompt)
	return err
}

// Resize updates the PTY dimensions and notifies all attached observers.
func (h *Host) Resize(cols, rows, x, y uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("terminal dimensions must be positive")
	}
	size := pty.Winsize{Rows: rows, Cols: cols, X: x, Y: y}
	if err := pty.Setsize(h.pty, &size); err != nil {
		return err
	}
	h.mu.Lock()
	if h.cfg.State != nil {
		_ = h.cfg.State.Resize(size)
	}
	h.record.Cols = cols
	h.record.Rows = rows
	_ = h.persistRecordLocked()
	h.broadcastLocked(Frame{Kind: FrameSize, Seq: h.record.StreamSeq, Payload: encodeWinsize(size)})
	if state, ok := h.cfg.State.(interface{ SnapshotFrame() ([]byte, error) }); ok {
		if payload, err := state.SnapshotFrame(); err == nil {
			h.broadcastLocked(Frame{Kind: FrameScreen, Seq: h.record.StreamSeq, Payload: payload})
		}
	}
	h.mu.Unlock()
	return nil
}

// Interrupt sends SIGINT to the verified child process group.
func (h *Host) Interrupt() error {
	h.mu.Lock()
	child := h.record.Child
	h.mu.Unlock()
	if err := VerifyProcess(child); err != nil {
		return err
	}
	return syscall.Kill(-child.PGID, syscall.SIGINT)
}

// RequestStop verifies child identity, sends SIGTERM to its process group, and
// escalates to SIGKILL after grace if the verified process is still alive.
func (h *Host) RequestStop(grace time.Duration) error {
	h.mu.Lock()
	child := h.record.Child
	if h.finished {
		h.mu.Unlock()
		return nil
	}
	h.record.Status = "stopping"
	_ = h.persistRecordLocked()
	h.mu.Unlock()

	if err := VerifyProcess(child); err != nil {
		return err
	}
	if err := syscall.Kill(-child.PGID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if grace <= 0 {
		grace = 2 * time.Second
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-h.done:
		return nil
	case <-timer.C:
		if VerifyProcess(child) == nil {
			_ = syscall.Kill(-child.PGID, syscall.SIGKILL)
		}
		<-h.done
		return nil
	}
}

// Wait blocks until the child process exits and its exit receipt is persisted.
func (h *Host) Wait() Record {
	<-h.done
	return h.Record()
}

// Close shuts down the host HTTP server and removes the runtime socket.
func (h *Host) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := h.server.Shutdown(ctx)
	_ = h.listener.Close()
	_ = os.Remove(h.cfg.SocketPath)
	return err
}

func (h *Host) run(ctx context.Context) {
	defer close(h.done)
	unblock := context.AfterFunc(ctx, func() {
		h.mu.Lock()
		child := h.record.Child
		h.mu.Unlock()
		if VerifyProcess(child) == nil {
			_ = syscall.Kill(-child.PGID, syscall.SIGTERM)
		}
	})
	defer unblock()

	if h.screenWake != nil {
		go h.pumpScreen()
	}
	buf := make([]byte, maxPTYRead)
	for {
		n, err := h.pty.Read(buf)
		if n > 0 {
			h.publishOutput(buf[:n])
		}
		if err != nil {
			break
		}
	}
	_ = h.pty.Close()

	waitErr := h.cmd.Wait()
	exitCode := 0
	exitSignal := ""
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					exitSignal = status.Signal().String()
					exitCode = 128 + int(status.Signal())
				} else {
					exitCode = status.ExitStatus()
				}
			} else {
				exitCode = exitErr.ExitCode()
			}
		} else {
			exitCode = 1
		}
	}

	now := time.Now().UnixNano()
	h.mu.Lock()
	h.finished = true
	h.record.Status = "exited"
	h.record.ExitCode = &exitCode
	h.record.ExitSignal = exitSignal
	h.record.ExitedAt = now

	h.flushScreenLocked(false)
	h.persistScreenLocked(true)
	snap := h.snapshotLocked()
	h.cfg.State = nil
	h.scrollback = snap
	_ = writeFileAtomic(h.snapshotPath, snap, 0600)

	receipt := ExitReceipt{
		SessionID:  h.record.SessionID,
		AttemptID:  h.record.AttemptID,
		StartNonce: h.record.StartNonce,
		Child:      h.record.Child,
		ExitCode:   exitCode,
		ExitSignal: exitSignal,
		StreamSeq:  h.record.StreamSeq,
		ExitedAt:   now,
	}
	if data, err := json.MarshalIndent(receipt, "", "  "); err == nil {
		_ = writeFileAtomic(h.exitPath, data, 0600)
	}
	_ = h.persistRecordLocked()

	exitPayload, _ := json.Marshal(ExitEvent{
		ExitCode:   exitCode,
		ExitSignal: exitSignal,
		Seq:        h.record.StreamSeq,
	})
	h.broadcastLocked(Frame{Kind: FrameExit, Seq: h.record.StreamSeq, Payload: exitPayload})
	for _, obs := range h.observers {
		close(obs.out)
	}
	h.observers = make(map[string]*attachObserver)
	h.mu.Unlock()
}

func (h *Host) publishOutput(data []byte) {
	chunk := append([]byte(nil), data...)
	h.mu.Lock()
	defer h.mu.Unlock()

	h.record.StreamSeq += uint64(len(chunk))
	h.appendScrollbackLocked(chunk)
	if h.cfg.State != nil {
		var replyTo *os.File
		if owner := h.observers[h.ownerID]; owner == nil || owner.screenOnly {
			replyTo = h.pty
		}
		h.cfg.State.Feed(chunk, replyTo)
		if h.screenWake != nil {
			select {
			case h.screenWake <- struct{}{}:
			default:
			}
		}
	}
	h.broadcastLocked(Frame{
		Kind:    FrameOutput,
		Seq:     h.record.StreamSeq,
		Payload: chunk,
	})
}

// pumpScreen coalesces PTY reads before copying changed cells out of Ghostty.
func (h *Host) pumpScreen() {
	ticker := time.NewTicker(screenInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-h.screenWake:
		}
		select {
		case <-h.done:
			return
		case <-ticker.C:
		}
		h.mu.Lock()
		h.flushScreenLocked(false)
		h.persistScreenLocked(false)
		h.mu.Unlock()
	}
}

func (h *Host) flushScreenLocked(full bool) {
	if len(h.observers) == 0 {
		return
	}
	if state, ok := h.cfg.State.(interface{ RenderFrame(bool) ([]byte, error) }); ok {
		if payload, err := state.RenderFrame(full); err == nil && len(payload) > 0 {
			h.broadcastLocked(Frame{Kind: FrameScreen, Seq: h.record.StreamSeq, Payload: payload})
		}
	}
}

func (h *Host) appendScrollbackLocked(chunk []byte) {
	if len(chunk) >= maxScrollback {
		h.scrollback = append(h.scrollback[:0], chunk[len(chunk)-maxScrollback:]...)
		return
	}
	h.scrollback = append(h.scrollback, chunk...)
	if len(h.scrollback) > maxScrollback {
		excess := len(h.scrollback) - maxScrollback
		copy(h.scrollback, h.scrollback[excess:])
		h.scrollback = h.scrollback[:maxScrollback]
	}
}

// Preview returns the terminal replay without attaching or claiming input.
func (h *Host) Preview() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked()
}

// PreviewFrame returns a full libghostty render-state frame for canvas previews.
func (h *Host) PreviewFrame() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.cfg.State.(interface{ SnapshotFrame() ([]byte, error) })
	if ok {
		frame, err := state.SnapshotFrame()
		if err == nil && len(frame) > 0 {
			return frame
		}
	}
	frame, _ := os.ReadFile(h.screenPath)
	return frame
}

// persistScreenLocked keeps Carson-compatible cell frames for right-panel previews after the
// live host is no longer in memory. The snapshot path is distinct from snapshot.bin, which keeps
// the ANSI replay used to resume a conversation.
func (h *Host) persistScreenLocked(final bool) {
	if h.cfg.State == nil || (!final && time.Since(h.screenSaved) < 2*time.Second) {
		return
	}
	state, ok := h.cfg.State.(interface{ SnapshotFrame() ([]byte, error) })
	if !ok {
		return
	}
	frame, err := state.SnapshotFrame()
	if err != nil || len(frame) == 0 {
		return
	}
	if writeFileAtomic(h.screenPath, frame, 0600) == nil {
		h.screenSaved = time.Now()
	}
}

func (h *Host) snapshotLocked() []byte {
	if h.cfg.State != nil {
		if replay, err := h.cfg.State.Replay(); err == nil {
			return replay
		}
	}
	return append([]byte(nil), h.scrollback...)
}

func (h *Host) broadcastLocked(frame Frame) {
	for id, obs := range h.observers {
		if frame.Kind == FrameOutput && obs.screenOnly {
			continue
		}
		select {
		case obs.out <- frame:
		default:
			close(obs.out)
			delete(h.observers, id)
		}
	}
}

func (h *Host) persistRecordLocked() error {
	data, err := json.MarshalIndent(h.record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(h.recordPath, data, 0600)
}

// LoadRecord reads a persisted host.json record from stateDir.
func LoadRecord(stateDir string) (Record, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "host.json"))
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// ArtifactInput describes an artifact to capture atomically with a completion event.
type ArtifactInput struct {
	Name string
	Kind string
	Data []byte
}

// ArtifactRef records a persisted artifact's location and digest.
type ArtifactRef struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
}

// OutboxRecord is a durable host-owned completion record awaiting daemon delivery.
type OutboxRecord struct {
	AttemptID string          `json:"attempt_id"`
	EventID   string          `json:"event_id"`
	Status    string          `json:"status"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Artifacts []ArtifactRef   `json:"artifacts,omitempty"`
	Digest    string          `json:"digest"`
	CreatedAt int64           `json:"created_at"`
	Delivered bool            `json:"delivered"`
}

// LocalReceipt confirms durable local capture of a completion event and its artifacts.
type LocalReceipt struct {
	AttemptID string `json:"attempt_id"`
	EventID   string `json:"event_id"`
	Digest    string `json:"digest"`
	SavedAt   int64  `json:"saved_at"`
}

// Outbox persists completion events and referenced artifacts under durable
// per-attempt storage before returning a local receipt.
type Outbox struct {
	root         string
	attemptID    string
	outboxDir    string
	artifactsDir string
}

// NewOutbox opens per-attempt outbox storage rooted at stateDir/attempts/<attemptID>.
func NewOutbox(stateDir, attemptID string) (*Outbox, error) {
	if stateDir == "" || attemptID == "" {
		return nil, fmt.Errorf("state_dir and attempt_id are required")
	}
	root := filepath.Join(stateDir, "attempts", attemptID)
	o := &Outbox{
		root:         root,
		attemptID:    attemptID,
		outboxDir:    filepath.Join(root, "outbox"),
		artifactsDir: filepath.Join(root, "artifacts"),
	}
	if err := os.MkdirAll(o.outboxDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.artifactsDir, 0700); err != nil {
		return nil, err
	}
	return o, nil
}

// Capture persists all referenced artifacts and the completion event record
// atomically before returning a LocalReceipt.
func (o *Outbox) Capture(eventID, status string, payload []byte, artifacts []ArtifactInput) (LocalReceipt, error) {
	if eventID == "" || status == "" {
		return LocalReceipt{}, fmt.Errorf("event_id and status are required")
	}
	refs := make([]ArtifactRef, 0, len(artifacts))
	for _, art := range artifacts {
		if art.Name == "" || filepath.Base(art.Name) != art.Name || art.Name == "." || art.Name == ".." {
			return LocalReceipt{}, fmt.Errorf("invalid artifact name %q", art.Name)
		}
		target := filepath.Join(o.artifactsDir, art.Name)
		if err := writeFileAtomic(target, art.Data, 0600); err != nil {
			return LocalReceipt{}, fmt.Errorf("persist artifact %s: %w", art.Name, err)
		}
		sum := sha256.Sum256(art.Data)
		refs = append(refs, ArtifactRef{
			Name:         art.Name,
			Kind:         art.Kind,
			RelativePath: filepath.Join("artifacts", art.Name),
			Digest:       hex.EncodeToString(sum[:]),
			Size:         int64(len(art.Data)),
		})
	}

	hasher := sha256.New()
	hasher.Write([]byte(status + "\x00"))
	hasher.Write(payload)
	digest := hex.EncodeToString(hasher.Sum(nil))
	now := time.Now().UnixNano()

	rec := OutboxRecord{
		AttemptID: o.attemptID,
		EventID:   eventID,
		Status:    status,
		Payload:   append([]byte(nil), payload...),
		Artifacts: refs,
		Digest:    digest,
		CreatedAt: now,
		Delivered: false,
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return LocalReceipt{}, err
	}
	recordPath := filepath.Join(o.outboxDir, eventID+".json")
	if err := writeFileAtomic(recordPath, raw, 0600); err != nil {
		return LocalReceipt{}, fmt.Errorf("persist outbox event: %w", err)
	}
	return LocalReceipt{
		AttemptID: o.attemptID,
		EventID:   eventID,
		Digest:    digest,
		SavedAt:   now,
	}, nil
}

// Pending returns all undelivered outbox records in creation order.
func (o *Outbox) Pending() ([]OutboxRecord, error) {
	entries, err := os.ReadDir(o.outboxDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []OutboxRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.outboxDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var rec OutboxRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, err
		}
		if !rec.Delivered {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}

// MarkDelivered marks an outbox event as delivered to the daemon.
func (o *Outbox) MarkDelivered(eventID string) error {
	path := filepath.Join(o.outboxDir, eventID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rec OutboxRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	rec.Delivered = true
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
