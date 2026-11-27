package localapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/romanvolkov/flows/desktop/theme"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
)

// CreateRunRequest launches an empty or prompted session root idempotently.
type CreateRunRequest struct {
	RequestID string          `json:"request_id"`
	Harness   harness.Kind    `json:"harness"`
	Prompt    string          `json:"prompt,omitempty"`
	WorkDir   string          `json:"work_dir,omitempty"`
	Profile   harness.Profile `json:"profile,omitempty"`
	Command   string          `json:"command,omitempty"`
	Args      []string        `json:"args,omitempty"`
}

// CreateRunResponse describes the committed root, attempt, and host socket.
type CreateRunResponse struct {
	RequestID  string `json:"request_id"`
	RunID      string `json:"run_id"`
	NodeID     string `json:"node_id"`
	AttemptID  string `json:"attempt_id"`
	SessionID  string `json:"session_id"`
	SocketPath string `json:"socket_path"`
	Status     string `json:"status"`
}

// LaunchIntent is persisted atomically with root/attempt creation so retries
// and daemon restarts converge on one root, one attempt, and one host.
type LaunchIntent struct {
	RequestID  string          `json:"request_id"`
	RunID      string          `json:"run_id"`
	NodeID     string          `json:"node_id"`
	AttemptID  string          `json:"attempt_id"`
	SessionID  string          `json:"session_id"`
	Harness    harness.Kind    `json:"harness"`
	Prompt     string          `json:"prompt,omitempty"`
	WorkDir    string          `json:"work_dir,omitempty"`
	Profile    harness.Profile `json:"profile,omitempty"`
	Command    string          `json:"command"`
	Args       []string        `json:"args,omitempty"`
	SocketPath string          `json:"socket_path"`
	State      string          `json:"state"` // "committed" | "started"
}

// RunManager coordinates idempotent run commits, per-attempt startup locks,
// and host adoption/launch.
type RunManager struct {
	home     paths.Home
	store    *store.Store
	sessions *SessionManager

	mu      sync.Mutex
	hosts   map[string]*host.Host
	watched map[string]bool
}

// NewRunManager creates a RunManager backed by home, st, and sessions.
func NewRunManager(home paths.Home, st *store.Store, sessions *SessionManager) *RunManager {
	return &RunManager{
		home:     home,
		store:    st,
		sessions: sessions,
		hosts:    make(map[string]*host.Host),
		watched:  make(map[string]bool),
	}
}

func (m *RunManager) intentsDir() string {
	return filepath.Join(m.home.State, "intents")
}

func (m *RunManager) intentByRequestPath(requestID string) string {
	return filepath.Join(m.intentsDir(), "req-"+safeID(requestID)+".json")
}

func (m *RunManager) intentByAttemptPath(attemptID string) string {
	return filepath.Join(m.intentsDir(), "att-"+safeID(attemptID)+".json")
}

// CommitIntent atomically commits the request ID, run root, session node,
// attempt, and durable launch intent without yet spawning the host.
func (m *RunManager) CommitIntent(ctx context.Context, req CreateRunRequest) (LaunchIntent, error) {
	if req.RequestID == "" {
		return LaunchIntent{}, fmt.Errorf("request_id is required")
	}
	if req.Harness == "" {
		req.Harness = harness.KindClaude
	}
	if err := os.MkdirAll(m.intentsDir(), 0700); err != nil {
		return LaunchIntent{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Idempotent check: if request_id already has a committed intent, return it.
	if existing, err := m.loadIntent(m.intentByRequestPath(req.RequestID)); err == nil {
		return existing, nil
	}

	cmd := req.Command
	if cmd == "" {
		settings, err := config.LoadSettings(m.home.Settings())
		if err != nil {
			return LaunchIntent{}, err
		}
		configured := config.ProfileFromSettings(req.Harness, settings)
		profile := config.ResolveEffectiveProfile(req.Harness, &req.Profile, nil, &configured)
		adapter, err := harness.ForKind(req.Harness)
		if err != nil {
			return LaunchIntent{}, err
		}
		caps, err := adapter.Probe(ctx, profile.Executable)
		if err != nil {
			return LaunchIntent{}, err
		}
		if req.WorkDir == "" {
			req.WorkDir = settings.DefaultDirectory
		}
		if req.WorkDir == "" {
			base := filepath.Join(m.home.State, "chats")
			if err := os.MkdirAll(base, 0700); err != nil {
				return LaunchIntent{}, err
			}
			req.WorkDir, err = os.MkdirTemp(base, "session-")
			if err != nil {
				return LaunchIntent{}, err
			}
		} else {
			if req.WorkDir == "~" {
				req.WorkDir = m.home.Environment()["HOME"]
			} else if tail, ok := strings.CutPrefix(req.WorkDir, "~/"); ok {
				req.WorkDir = filepath.Join(m.home.Environment()["HOME"], tail)
			}
			fi, err := os.Stat(req.WorkDir)
			if err != nil {
				return LaunchIntent{}, err
			}
			if !filepath.IsAbs(req.WorkDir) || !fi.IsDir() {
				return LaunchIntent{}, errors.New("choose an absolute working directory")
			}
		}
		resolved, err := adapter.BuildLaunch(caps, harness.LaunchSpec{Profile: profile, WorkDir: req.WorkDir, Prompt: req.Prompt})
		if err != nil {
			return LaunchIntent{}, err
		}
		cmd = resolved.Argv[0]
		req.Args = append(resolved.Argv[1:], req.Args...)
		req.Prompt = resolved.PTYPrompt
		req.Profile = profile
	}
	runID, err := m.store.CreateRun(ctx, nil)
	if err != nil {
		return LaunchIntent{}, err
	}
	node, err := m.store.AddNode(ctx, store.NewNode{
		RunID: runID,
		Kind:  "session",
		Name:  string(req.Harness),
	})
	if err != nil {
		return LaunchIntent{}, err
	}
	att, err := m.store.StartAttempt(ctx, node.ID)
	if err != nil {
		return LaunchIntent{}, err
	}

	sessID := "sess-" + shortHex(8)
	sock := filepath.Join(m.home.Runtime, "h-"+sessID+".sock")

	intent := LaunchIntent{
		RequestID:  req.RequestID,
		RunID:      runID,
		NodeID:     node.ID,
		AttemptID:  att.ID,
		SessionID:  sessID,
		Harness:    req.Harness,
		Prompt:     req.Prompt,
		WorkDir:    req.WorkDir,
		Profile:    req.Profile,
		Command:    cmd,
		Args:       append([]string(nil), req.Args...),
		SocketPath: sock,
		State:      "committed",
	}
	if err := m.saveIntent(intent); err != nil {
		return LaunchIntent{}, err
	}
	return intent, nil
}

// EnsureHostStarted acquires an exclusive per-attempt startup lock and either
// adopts an already-started host for the attempt or starts a new host process.
func (m *RunManager) EnsureHostStarted(ctx context.Context, intent LaunchIntent) (CreateRunResponse, error) {
	if err := m.home.InitRuntime(); err != nil {
		return CreateRunResponse{}, err
	}
	lock, err := TryLock(filepath.Join(m.home.Runtime, "attempt-"+safeID(intent.AttemptID)+".lock"))
	if err != nil {
		return CreateRunResponse{}, err
	}
	defer lock.Close()

	// Check if already tracked in-process.
	m.mu.Lock()
	if h, ok := m.hosts[intent.AttemptID]; ok {
		m.mu.Unlock()
		m.watchHost(h)
		rec := h.Record()
		return CreateRunResponse{
			RequestID:  intent.RequestID,
			RunID:      intent.RunID,
			NodeID:     intent.NodeID,
			AttemptID:  intent.AttemptID,
			SessionID:  rec.SessionID,
			SocketPath: rec.SocketPath,
			Status:     rec.Status,
		}, nil
	}
	m.mu.Unlock()

	hostStateDir := filepath.Join(m.home.State, "hosts", intent.SessionID)
	// Check if host already started on disk before daemon registered it.
	if insp, err := host.InspectHost(hostStateDir, m.home.State); err == nil && insp.Health == host.HealthLive {
		m.sessions.mu.Lock()
		m.sessions.adopted[insp.Record.SessionID] = insp.Record
		m.sessions.leases[insp.Record.AttemptID] = host.LeaseHeld
		m.sessions.mu.Unlock()
		m.watchAdoptedHost(context.Background(), insp.Record)

		intent.State = "started"
		_ = m.saveIntent(intent)
		return CreateRunResponse{
			RequestID:  intent.RequestID,
			RunID:      intent.RunID,
			NodeID:     intent.NodeID,
			AttemptID:  intent.AttemptID,
			SessionID:  insp.Record.SessionID,
			SocketPath: insp.Record.SocketPath,
			Status:     insp.Record.Status,
		}, nil
	}

	h, err := host.Start(context.Background(), host.Config{
		SessionID:     intent.SessionID,
		AttemptID:     intent.AttemptID,
		StateDir:      hostStateDir,
		SocketPath:    intent.SocketPath,
		WorkDir:       intent.WorkDir,
		Command:       intent.Command,
		Args:          intent.Args,
		InitialPrompt: intent.Prompt,
		InitialScreen: []byte(theme.TerminalOSC()),
	})
	if err != nil {
		return CreateRunResponse{}, err
	}
	if err := h.WaitCheckoutReady(ctx); err != nil {
		_ = h.Close()
		return CreateRunResponse{}, err
	}

	rec := h.Record()
	m.mu.Lock()
	m.hosts[intent.AttemptID] = h
	m.mu.Unlock()
	m.watchHost(h)

	m.sessions.mu.Lock()
	m.sessions.adopted[rec.SessionID] = rec
	m.sessions.leases[rec.AttemptID] = host.LeaseHeld
	m.sessions.mu.Unlock()

	intent.State = "started"
	_ = m.saveIntent(intent)

	return CreateRunResponse{
		RequestID:  intent.RequestID,
		RunID:      intent.RunID,
		NodeID:     intent.NodeID,
		AttemptID:  intent.AttemptID,
		SessionID:  rec.SessionID,
		SocketPath: rec.SocketPath,
		Status:     rec.Status,
	}, nil
}

// WatchAdoptedHosts follows sessions that survived a daemon restart. Newly
// launched sessions are followed directly through their Host.Wait result.
func (m *RunManager) WatchAdoptedHosts(ctx context.Context) {
	for _, session := range m.sessions.List() {
		if session.Status == "exited" {
			continue
		}
		rec, err := host.LoadRecord(filepath.Join(m.home.State, "hosts", session.SessionID))
		if err == nil {
			m.watchAdoptedHost(ctx, rec)
		}
	}
}

func (m *RunManager) watchHost(h *host.Host) {
	rec := h.Record()
	m.mu.Lock()
	if m.watched[rec.AttemptID] {
		m.mu.Unlock()
		return
	}
	m.watched[rec.AttemptID] = true
	m.mu.Unlock()
	go func() { m.completeHostExit(h.Wait()) }()
}

func (m *RunManager) watchAdoptedHost(ctx context.Context, rec host.Record) {
	m.mu.Lock()
	if m.watched[rec.AttemptID] {
		m.mu.Unlock()
		return
	}
	m.watched[rec.AttemptID] = true
	m.mu.Unlock()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := host.LoadRecord(filepath.Join(m.home.State, "hosts", rec.SessionID))
				if err == nil && (current.Status == "exited" || current.ExitCode != nil) {
					m.completeHostExit(current)
					return
				}
			}
		}
	}()
}

func (m *RunManager) completeHostExit(rec host.Record) {
	if rec.AttemptID == "" || rec.ExitCode == nil {
		return
	}
	status := store.Succeeded
	if *rec.ExitCode != 0 {
		status = store.Failed
	}
	eventID := "host-exit-" + rec.StartNonce
	payload, _ := json.Marshal(struct {
		ExitCode   int    `json:"exit_code"`
		ExitSignal string `json:"signal"`
	}{*rec.ExitCode, rec.ExitSignal})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.store.Complete(ctx, rec.AttemptID, eventID, status, payload); err == nil && m.sessions != nil {
		m.sessions.RecordExit(rec)
	}
}

// LaunchSession commits the intent and ensures the host is started or adopted.
func (m *RunManager) LaunchSession(ctx context.Context, req CreateRunRequest) (CreateRunResponse, error) {
	intent, err := m.CommitIntent(ctx, req)
	if err != nil {
		return CreateRunResponse{}, err
	}
	return m.EnsureHostStarted(ctx, intent)
}

// ResumeSession creates a new attempt on the existing node and resumes the
// harness conversation captured in the preceding attempt's terminal screen.
func (m *RunManager) ResumeSession(ctx context.Context, nodeID string) (CreateRunResponse, error) {
	node, err := m.store.Node(ctx, nodeID)
	if err != nil {
		return CreateRunResponse{}, err
	}
	if node.AttemptID == nil {
		return CreateRunResponse{}, errors.New("node has no previous attempt to resume")
	}
	previous, err := m.loadIntent(m.intentByAttemptPath(*node.AttemptID))
	if err != nil {
		return CreateRunResponse{}, fmt.Errorf("load previous harness session: %w", err)
	}
	adapter, err := harness.ForKind(previous.Harness)
	if err != nil {
		return CreateRunResponse{}, err
	}
	caps, err := adapter.Probe(ctx, previous.Profile.Executable)
	if err != nil {
		return CreateRunResponse{}, err
	}
	if !caps.SupportsResume {
		return CreateRunResponse{}, fmt.Errorf("%s does not support conversation resume", previous.Harness)
	}

	m.mu.Lock()
	active := m.hosts[*node.AttemptID]
	m.mu.Unlock()
	var screen []byte
	if active != nil {
		screen = active.Preview()
	} else {
		screen, err = os.ReadFile(filepath.Join(m.home.State, "hosts", previous.SessionID, "snapshot.bin"))
		if err != nil {
			return CreateRunResponse{}, fmt.Errorf("read previous session screen: %w", err)
		}
	}
	conversationID, _, ok := adapter.ParseConversationRef(screen)
	if !ok {
		return CreateRunResponse{}, fmt.Errorf("could not find a %s conversation ID in the previous session screen", previous.Harness)
	}
	resolved, err := adapter.BuildLaunch(caps, harness.LaunchSpec{
		Profile: previous.Profile, WorkDir: previous.WorkDir, ConversationID: conversationID, Resume: true,
	})
	if err != nil {
		return CreateRunResponse{}, err
	}
	attempt, err := m.store.ResumeAttempt(ctx, nodeID)
	if err != nil {
		return CreateRunResponse{}, err
	}
	sessionID := "sess-" + shortHex(8)
	intent := LaunchIntent{
		RequestID: "resume-" + attempt.ID, RunID: node.RunID, NodeID: node.ID,
		AttemptID: attempt.ID, SessionID: sessionID, Harness: previous.Harness,
		WorkDir: previous.WorkDir, Profile: previous.Profile, Command: resolved.Argv[0],
		Args: append([]string(nil), resolved.Argv[1:]...), SocketPath: filepath.Join(m.home.Runtime, "h-"+sessionID+".sock"), State: "committed",
	}
	if err := m.saveIntent(intent); err != nil {
		_ = m.store.Transition(ctx, nodeID, store.Interrupted, nil)
		return CreateRunResponse{}, fmt.Errorf("save resumed launch: %w", err)
	}
	response, err := m.EnsureHostStarted(ctx, intent)
	if err != nil {
		_ = m.store.Transition(ctx, nodeID, store.Interrupted, nil)
		return CreateRunResponse{}, err
	}
	return response, nil
}

// ReconcileIntents reconciles unfinished committed intents on daemon startup.
func (m *RunManager) ReconcileIntents(ctx context.Context) error {
	entries, err := os.ReadDir(m.intentsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "req-") {
			continue
		}
		intent, err := m.loadIntent(filepath.Join(m.intentsDir(), entry.Name()))
		if err != nil {
			continue
		}
		if intent.State == "committed" {
			if _, err := m.EnsureHostStarted(ctx, intent); err != nil {
				continue // do not abort daemon startup if one stale intent cannot restart
			}
		}
	}
	return nil
}

// CloseHosts shuts down all in-process hosts owned by RunManager.
func (m *RunManager) CloseHosts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.hosts {
		_ = h.Close()
		delete(m.hosts, id)
	}
}

func (m *RunManager) saveIntent(intent LaunchIntent) error {
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(m.intentByRequestPath(intent.RequestID), data); err != nil {
		return err
	}
	return writeAtomic(m.intentByAttemptPath(intent.AttemptID), data)
}

func writeAtomic(dst string, data []byte) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (m *RunManager) loadIntent(path string) (LaunchIntent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LaunchIntent{}, err
	}
	var intent LaunchIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return LaunchIntent{}, err
	}
	return intent, nil
}

func handleRuns(w http.ResponseWriter, r *http.Request, runs *RunManager) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "run manager not configured")
		return
	}
	var req CreateRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid run request JSON")
		return
	}
	resp, err := runs.LaunchSession(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "launch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleNodeResume(w http.ResponseWriter, r *http.Request, runs *RunManager, nodeID string) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "run manager not configured")
		return
	}
	response, err := runs.ResumeSession(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusConflict, "resume_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func safeID(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func shortHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// preview returns the live replay buffer or the final persisted screen for an attempt.
func (m *RunManager) preview(w http.ResponseWriter, attempt string) {
	if attempt == "" || safeID(attempt) != attempt {
		writeError(w, 404, "not_found", "Unknown attempt")
		return
	}
	m.mu.Lock()
	h := m.hosts[attempt]
	m.mu.Unlock()
	var data []byte
	var frame []byte
	var err error
	if h != nil {
		data = h.Preview()
		frame = h.PreviewFrame()
	} else {
		var intent LaunchIntent
		intent, err = m.loadIntent(m.intentByAttemptPath(attempt))
		if err == nil {
			data, err = os.ReadFile(filepath.Join(m.home.State, "hosts", intent.SessionID, "snapshot.bin"))
			frame, _ = os.ReadFile(filepath.Join(m.home.State, "hosts", intent.SessionID, "screen.json"))
		}
	}
	if errors.Is(err, os.ErrNotExist) && len(frame) == 0 {
		writeJSON(w, 200, map[string]string{"screen": ""})
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		writeError(w, 500, "preview_failed", "Could not read the session preview")
		return
	}
	writeJSON(w, 200, map[string][]byte{"screen": data, "frame": frame})
}
