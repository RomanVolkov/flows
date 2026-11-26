package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

// LeaseStatus describes whether a checkout workspace is safe to reuse.
type LeaseStatus string

const (
	LeaseHeld       LeaseStatus = "held"
	LeaseReleased   LeaseStatus = "released"
	LeaseUnresolved LeaseStatus = "unresolved"
)

// HostHealth classifies the state of a persisted host on daemon restart.
type HostHealth string

const (
	HealthMissing HostHealth = "missing"
	HealthLive    HostHealth = "live"
	HealthExited  HostHealth = "exited"
	HealthLost    HostHealth = "lost"
)

// Inspection summarizes the verified state of a persisted host directory.
type Inspection struct {
	StateDir    string
	Record      Record
	Health      HostHealth
	Lease       LeaseStatus
	LeaseReason string
	Pending     []OutboxRecord
	ExitReceipt *ExitReceipt
}

// ReconcileReport summarizes daemon startup recovery across all persisted hosts.
type ReconcileReport struct {
	Adopted     []Record
	Completed   []string
	Interrupted []string
	Unresolved  []string
}

// InspectHost reads persisted host state, verifies host and child birth identities,
// checks for pending outbox completions, and determines checkout lease safety.
func InspectHost(stateDir, outboxRoot string) (Inspection, error) {
	rec, err := LoadRecord(stateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Inspection{StateDir: stateDir, Health: HealthMissing, Lease: LeaseReleased}, nil
		}
		return Inspection{StateDir: stateDir, Health: HealthLost, Lease: LeaseUnresolved, LeaseReason: err.Error()}, err
	}

	insp := Inspection{
		StateDir: stateDir,
		Record:   rec,
		Lease:    LeaseHeld,
	}

	if outboxRoot != "" && rec.AttemptID != "" {
		if ob, err := NewOutbox(outboxRoot, rec.AttemptID); err == nil {
			if pending, err := ob.Pending(); err == nil {
				insp.Pending = pending
			}
		}
	}

	if rec.Status == "exited" || rec.ExitCode != nil {
		insp.Health = HealthExited
		insp.Lease = confirmStoppedLease(rec.Child)
		return insp, nil
	}

	hostErr := VerifyProcess(rec.Host)
	childErr := VerifyProcess(rec.Child)
	socketAlive := probeSocket(rec.SocketPath)

	if hostErr == nil && childErr == nil && socketAlive {
		insp.Health = HealthLive
		insp.Lease = LeaseHeld
		return insp, nil
	}

	// Host or child is no longer verifiable as the original live process.
	insp.Health = HealthLost
	insp.Lease, insp.LeaseReason = evaluateLostLease(rec, hostErr, childErr)
	return insp, nil
}

func probeSocket(socketPath string) bool {
	if socketPath == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", socketPath, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func confirmStoppedLease(child ProcessIdentity) LeaseStatus {
	if child.PID <= 0 {
		return LeaseUnresolved
	}
	if err := VerifyProcess(child); err == nil {
		return LeaseHeld
	}
	if child.PGID > 0 {
		if err := syscall.Kill(-child.PGID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			return LeaseUnresolved
		}
	}
	return LeaseReleased
}

func evaluateLostLease(rec Record, hostErr, childErr error) (LeaseStatus, string) {
	// If the original child is still alive, the lease is held/unresolved.
	if childErr == nil {
		return LeaseUnresolved, "child process still alive after host loss"
	}
	// If PID was reused (birth time changed), we cannot confirm descendants stopped.
	if hostErr != nil && isBirthMismatch(hostErr) {
		return LeaseUnresolved, "host PID reused; identity uncertain"
	}
	if isBirthMismatch(childErr) {
		return LeaseUnresolved, "child PID reused; identity uncertain"
	}
	// Check if the managed process group still has any live descendants.
	if rec.Child.PGID > 0 {
		if err := syscall.Kill(-rec.Child.PGID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			return LeaseUnresolved, "managed process group still has live descendants"
		}
	}
	return LeaseReleased, ""
}

func isBirthMismatch(err error) bool {
	return err != nil && errors.Is(err, ErrProcessMismatch) &&
		(containsText(err.Error(), "birth time changed") || containsText(err.Error(), "pgid changed"))
}

func containsText(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexString(s, sub) >= 0
}

func indexString(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ReconcileAll inspects all persisted host directories under hostsRoot,
// reconciles pending completion outbox records first, adopts live hosts,
// and marks lost/unresolved work as interrupted in the store before new work starts.
func ReconcileAll(ctx context.Context, st *store.Store, hostsRoot, outboxRoot string) (ReconcileReport, error) {
	var report ReconcileReport

	// 1. Reconcile any pending completion outbox records first (even if host dir is gone).
	if outboxRoot != "" {
		attemptsDir := filepath.Join(outboxRoot, "attempts")
		if entries, err := os.ReadDir(attemptsDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				attemptID := entry.Name()
				ob, err := NewOutbox(outboxRoot, attemptID)
				if err != nil {
					continue
				}
				pending, err := ob.Pending()
				if err != nil {
					return report, err
				}
				for _, rec := range pending {
					status := store.Status(rec.Status)
					if status != store.Succeeded && status != store.Failed {
						status = store.Failed
					}
					if _, err := st.Complete(ctx, rec.AttemptID, rec.EventID, status, rec.Payload); err != nil {
						if !errors.Is(err, store.ErrCompletionConflict) {
							return report, fmt.Errorf("reconcile completion %s/%s: %w", rec.AttemptID, rec.EventID, err)
						}
					}
					if err := ob.MarkDelivered(rec.EventID); err != nil {
						return report, err
					}
					report.Completed = append(report.Completed, rec.AttemptID)
				}
			}
		}
	}

	// 2. Inspect persisted host directories for adoption, missed exits, or host loss.
	if hostsRoot == "" {
		return report, nil
	}
	entries, err := os.ReadDir(hostsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return report, nil
		}
		return report, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stateDir := filepath.Join(hostsRoot, entry.Name())
		insp, err := InspectHost(stateDir, outboxRoot)
		if err != nil && insp.Health == HealthMissing {
			continue
		}

		switch insp.Health {
		case HealthLive:
			report.Adopted = append(report.Adopted, insp.Record)

		case HealthExited:
			if insp.Record.AttemptID != "" {
				status := store.Succeeded
				if insp.Record.ExitCode != nil && *insp.Record.ExitCode != 0 {
					status = store.Failed
				}
				eventID := "host-exit-" + insp.Record.StartNonce
				payload := []byte(fmt.Sprintf(`{"exit_code":%d,"signal":%q}`, derefInt(insp.Record.ExitCode), insp.Record.ExitSignal))
				if _, err := st.Complete(ctx, insp.Record.AttemptID, eventID, status, payload); err == nil {
					report.Completed = append(report.Completed, insp.Record.AttemptID)
				}
			}

		case HealthLost:
			if insp.Lease == LeaseUnresolved {
				report.Unresolved = append(report.Unresolved, insp.Record.AttemptID)
			}
			if insp.Record.AttemptID != "" {
				if err := markAttemptInterrupted(ctx, st, insp.Record.AttemptID); err == nil {
					report.Interrupted = append(report.Interrupted, insp.Record.AttemptID)
				}
			}
		}
	}
	return report, nil
}

func markAttemptInterrupted(ctx context.Context, st *store.Store, attemptID string) error {
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, node := range snap.Nodes {
		if node.AttemptID != nil && *node.AttemptID == attemptID {
			if node.Status == store.Running || node.Status == store.WaitingForInput || node.Status == store.Stopping {
				return st.Transition(ctx, node.ID, store.Interrupted, nil)
			}
		}
	}
	return nil
}

func derefInt(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
