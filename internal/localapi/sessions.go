package localapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

// SessionSummary describes an adopted or tracked session in the daemon.
type SessionSummary struct {
	SessionID   string               `json:"session_id"`
	AttemptID   string               `json:"attempt_id"`
	StartNonce  string               `json:"start_nonce"`
	SocketPath  string               `json:"socket_path"`
	StreamSeq   uint64               `json:"stream_seq"`
	Status      string               `json:"status"`
	LeaseStatus host.LeaseStatus     `json:"lease_status"`
	Host        host.ProcessIdentity `json:"host"`
	Child       host.ProcessIdentity `json:"child"`
}

// SessionManager tracks adopted hosts, checkout leases, and stop acknowledgements.
type SessionManager struct {
	store      *store.Store
	hostsRoot  string
	outboxRoot string

	mu      sync.Mutex
	adopted map[string]host.Record
	leases  map[string]host.LeaseStatus
}

// NewSessionManager creates a SessionManager rooted at stateDir.
func NewSessionManager(st *store.Store, stateDir string) *SessionManager {
	return &SessionManager{
		store:      st,
		hostsRoot:  filepath.Join(stateDir, "hosts"),
		outboxRoot: stateDir,
		adopted:    make(map[string]host.Record),
		leases:     make(map[string]host.LeaseStatus),
	}
}

// Reconcile adopts surviving hosts, reconciles pending completion records, and
// records interruptions before new work starts.
func (m *SessionManager) Reconcile(ctx context.Context) (host.ReconcileReport, error) {
	report, err := host.ReconcileAll(ctx, m.store, m.hostsRoot, m.outboxRoot)
	if err != nil {
		return report, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range report.Adopted {
		m.adopted[rec.SessionID] = rec
		m.leases[rec.AttemptID] = host.LeaseHeld
	}
	for _, attemptID := range report.Unresolved {
		m.leases[attemptID] = host.LeaseUnresolved
	}
	return report, nil
}

// Adopted returns the adopted host record for sessionID if present and still verified.
func (m *SessionManager) Adopted(sessionID string) (host.Record, bool) {
	m.mu.Lock()
	rec, ok := m.adopted[sessionID]
	m.mu.Unlock()
	if !ok {
		return host.Record{}, false
	}
	if err := host.VerifyProcess(rec.Host); err != nil {
		return host.Record{}, false
	}
	return rec, true
}

// LeaseForAttempt returns the checkout lease status for an attempt.
func (m *SessionManager) LeaseForAttempt(attemptID string) host.LeaseStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status, ok := m.leases[attemptID]; ok {
		return status
	}
	return host.LeaseReleased
}

// List returns summaries for all adopted sessions.
func (m *SessionManager) List() []SessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionSummary, 0, len(m.adopted))
	for _, rec := range m.adopted {
		lease := m.leases[rec.AttemptID]
		if lease == "" {
			lease = host.LeaseHeld
		}
		out = append(out, SessionSummary{
			SessionID:   rec.SessionID,
			AttemptID:   rec.AttemptID,
			StartNonce:  rec.StartNonce,
			SocketPath:  rec.SocketPath,
			StreamSeq:   rec.StreamSeq,
			Status:      rec.Status,
			LeaseStatus: lease,
			Host:        rec.Host,
			Child:       rec.Child,
		})
	}
	return out
}

// AcknowledgeStop moves a node through Stopping to Cancelled after verifying
// host/child stop. Repeated acknowledgements for the same attempt are idempotent.
func (m *SessionManager) AcknowledgeStop(ctx context.Context, nodeID, attemptID string) error {
	node, err := m.store.Node(ctx, nodeID)
	if err != nil {
		return err
	}
	if node.Status == store.Cancelled {
		// Repeated stop acknowledgement is idempotent.
		m.releaseLease(attemptID)
		return nil
	}
	if node.Status == store.Running || node.Status == store.WaitingForInput {
		if err := m.store.Transition(ctx, nodeID, store.Stopping, nil); err != nil {
			return err
		}
	}

	// Verify whether the host/child is still running; if live, request stop.
	m.mu.Lock()
	var rec host.Record
	var found bool
	for _, r := range m.adopted {
		if r.AttemptID == attemptID {
			rec = r
			found = true
			break
		}
	}
	m.mu.Unlock()

	if found && host.VerifyProcess(rec.Child) == nil {
		_ = syscall.Kill(-rec.Child.PGID, syscall.SIGTERM)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if host.VerifyProcess(rec.Child) != nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if host.VerifyProcess(rec.Child) == nil {
			_ = syscall.Kill(-rec.Child.PGID, syscall.SIGKILL)
		}
	}

	if err := m.store.Transition(ctx, nodeID, store.Cancelled, nil); err != nil {
		if errors.Is(err, store.ErrTransition) {
			latest, readErr := m.store.Node(ctx, nodeID)
			if readErr == nil && latest.Status == store.Cancelled {
				m.releaseLease(attemptID)
				return nil
			}
		}
		return err
	}
	m.releaseLease(attemptID)
	return nil
}

func (m *SessionManager) releaseLease(attemptID string) {
	if attemptID == "" {
		return
	}
	m.mu.Lock()
	m.leases[attemptID] = host.LeaseReleased
	m.mu.Unlock()
}

func handleSessions(w http.ResponseWriter, r *http.Request, mgr *SessionManager) {
	if mgr == nil {
		writeJSON(w, http.StatusOK, []SessionSummary{})
		return
	}
	if r.URL.Path == "/v1/sessions" {
		if method(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, mgr.List())
		}
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	sessionID, action, hasAction := strings.Cut(trimmed, "/")
	if !hasAction || sessionID == "" {
		writeError(w, http.StatusNotFound, "not_found", "unknown session endpoint")
		return
	}
	switch action {
	case "stop":
		if !method(w, r, http.MethodPost) {
			return
		}
		rec, ok := mgr.Adopted(sessionID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		snap, err := mgr.store.Snapshot(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		var nodeID string
		for _, n := range snap.Nodes {
			if n.AttemptID != nil && *n.AttemptID == rec.AttemptID {
				nodeID = n.ID
				break
			}
		}
		if nodeID == "" {
			writeError(w, http.StatusNotFound, "not_found", "active node for session not found")
			return
		}
		if err := mgr.AcknowledgeStop(r.Context(), nodeID, rec.AttemptID); err != nil {
			writeError(w, http.StatusConflict, "stop_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown session action")
	}
}
