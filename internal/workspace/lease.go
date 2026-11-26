// Package workspace manages isolated Git worktrees, group-owned checkout leases,
// stale-head checks, and remote ref reconciliation.
package workspace

import (
	"errors"
	"fmt"
	"sync"

	"github.com/romanvolkov/flows/internal/host"
)

var (
	ErrCheckoutBlocked = errors.New("checkout lease blocked while previous writer is still running or uncertain")
	ErrActiveSession   = errors.New("cannot dispose workspace while session or writer is active")
	ErrNotReleasable   = errors.New("workspace is not marked releasable")
)

// LeaseState tracks group-level checkout ownership across retries.
type LeaseState struct {
	WorkspaceID  string                `json:"workspace_id"`
	GroupID      string                `json:"group_id"`
	AttemptID    string                `json:"attempt_id"`
	CheckoutPath string                `json:"checkout_path"`
	Branch       string                `json:"branch"`
	BaseRev      string                `json:"base_rev"`
	WorkingRev   string                `json:"working_rev,omitempty"`
	Writer       *host.ProcessIdentity `json:"writer,omitempty"`
	Releasable   bool                  `json:"releasable"`
	Disposed     bool                  `json:"disposed"`
}

// LeaseManager separates group checkout ownership from attempt identity so
// retries can reuse deliberate changes once the previous writer stops.
type LeaseManager struct {
	mu     sync.Mutex
	leases map[string]*LeaseState
}

// NewLeaseManager creates a LeaseManager.
func NewLeaseManager() *LeaseManager {
	return &LeaseManager{
		leases: make(map[string]*LeaseState),
	}
}

// Acquire claims or reuses workspaceID for groupID and attemptID.
// If a previous writer is still alive (or its identity is uncertain), Acquire
// returns ErrCheckoutBlocked so the original checkout stays intact.
func (m *LeaseManager) Acquire(workspaceID, groupID, attemptID, checkoutPath, branch, baseRev string, writer *host.ProcessIdentity) (*LeaseState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.leases[workspaceID]; ok && !existing.Disposed {
		if existing.GroupID != groupID {
			return nil, fmt.Errorf("%w: owned by group %s", ErrCheckoutBlocked, existing.GroupID)
		}
		if existing.Writer != nil && host.VerifyProcess(*existing.Writer) == nil {
			return nil, fmt.Errorf("%w: writer pid %d still active", ErrCheckoutBlocked, existing.Writer.PID)
		}
		existing.AttemptID = attemptID
		existing.Writer = writer
		return existing, nil
	}

	ls := &LeaseState{
		WorkspaceID:  workspaceID,
		GroupID:      groupID,
		AttemptID:    attemptID,
		CheckoutPath: checkoutPath,
		Branch:       branch,
		BaseRev:      baseRev,
		Writer:       writer,
	}
	m.leases[workspaceID] = ls
	return ls, nil
}

// ReleaseWriter clears the active writer after confirming it has stopped.
func (m *LeaseManager) ReleaseWriter(workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.leases[workspaceID]
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceID)
	}
	if ls.Writer != nil && host.VerifyProcess(*ls.Writer) == nil {
		return fmt.Errorf("%w: writer pid %d still running", ErrCheckoutBlocked, ls.Writer.PID)
	}
	ls.Writer = nil
	return nil
}

// CanDispose checks whether workspaceID is explicitly releasable and has no active writer.
func (m *LeaseManager) CanDispose(workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.leases[workspaceID]
	if !ok {
		return nil
	}
	if !ls.Releasable {
		return ErrNotReleasable
	}
	if ls.Writer != nil && host.VerifyProcess(*ls.Writer) == nil {
		return ErrActiveSession
	}
	return nil
}

// MarkDisposed marks workspaceID disposed after confirming it is releasable and stopped.
func (m *LeaseManager) MarkDisposed(workspaceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.leases[workspaceID]
	if !ok {
		return nil
	}
	if !ls.Releasable {
		return ErrNotReleasable
	}
	if ls.Writer != nil && host.VerifyProcess(*ls.Writer) == nil {
		return ErrActiveSession
	}
	ls.Disposed = true
	return nil
}
