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

	mu    sync.Mutex
	hosts map[string]*host.Host
}

// NewRunManager creates a RunManager backed by home, st, and sessions.
func NewRunManager(home paths.Home, st *store.Store, sessions *SessionManager) *RunManager {
	return &RunManager{
		home:     home,
		store:    st,
		sessions: sessions,
		hosts:    make(map[string]*host.Host),
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

	// Idempotent check: if request_id already has a committed intent, return it.
	if existing, err := m.loadIntent(m.intentByRequestPath(req.RequestID)); err == nil {
		return existing, nil
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
	cmd := req.Command
	if cmd == "" {
		cmd = string(req.Harness)
	}
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

// LaunchSession commits the intent and ensures the host is started or adopted.
func (m *RunManager) LaunchSession(ctx context.Context, req CreateRunRequest) (CreateRunResponse, error) {
	intent, err := m.CommitIntent(ctx, req)
	if err != nil {
		return CreateRunResponse{}, err
	}
	return m.EnsureHostStarted(ctx, intent)
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
				return err
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
	if err := os.WriteFile(m.intentByRequestPath(intent.RequestID), data, 0600); err != nil {
		return err
	}
	return os.WriteFile(m.intentByAttemptPath(intent.AttemptID), data, 0600)
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
