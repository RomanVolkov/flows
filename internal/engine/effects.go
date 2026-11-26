package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EffectState tracks an externally visible provider action across crashes.
type EffectState string

const (
	EffectPending        EffectState = "pending"
	EffectCommitted      EffectState = "committed"
	EffectNeedsAttention EffectState = "needs_attention"
)

// ExternalEffect records an idempotent provider write intent and its reconciled state.
type ExternalEffect struct {
	InvocationID   string      `json:"invocation_id"`
	AttemptID      string      `json:"attempt_id"`
	Action         string      `json:"action"`
	IdempotencyKey string      `json:"idempotency_key"`
	State          EffectState `json:"state"`
	RemoteRef      string      `json:"remote_ref,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	UpdatedAt      int64       `json:"updated_at"`
}

// RemoteLookupFunc checks whether an externally visible action already succeeded
// on the remote provider using its idempotency key.
type RemoteLookupFunc func(ctx context.Context, idempotencyKey string) (found bool, remoteRef string, err error)

// EffectJournal persists external action intents before execution and reconciles
// ambiguous outcomes using the same InvocationID.
type EffectJournal struct {
	mu  sync.Mutex
	dir string
}

// OpenEffectJournal initializes durable effect storage under dir.
func OpenEffectJournal(dir string) (*EffectJournal, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &EffectJournal{dir: dir}, nil
}

// RecordIntent persists an external effect intent before execution.
func (j *EffectJournal) RecordIntent(eff ExternalEffect) (ExternalEffect, error) {
	if eff.InvocationID == "" || eff.IdempotencyKey == "" {
		return ExternalEffect{}, fmt.Errorf("invocation_id and idempotency_key are required")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if existing, err := j.loadLocked(eff.InvocationID); err == nil {
		return existing, nil
	}
	eff.State = EffectPending
	eff.UpdatedAt = time.Now().UnixNano()
	if err := j.saveLocked(eff); err != nil {
		return ExternalEffect{}, err
	}
	return eff, nil
}

// MarkCommitted records a confirmed external effect receipt.
func (j *EffectJournal) MarkCommitted(invocationID, remoteRef string) (ExternalEffect, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	eff, err := j.loadLocked(invocationID)
	if err != nil {
		return ExternalEffect{}, err
	}
	eff.State = EffectCommitted
	eff.RemoteRef = remoteRef
	eff.Reason = ""
	eff.UpdatedAt = time.Now().UnixNano()
	return eff, j.saveLocked(eff)
}

// Reconcile checks a pending effect via remoteLookup. If the remote state cannot
// be determined, it places the effect in EffectNeedsAttention before any retry.
func (j *EffectJournal) Reconcile(ctx context.Context, invocationID string, lookup RemoteLookupFunc) (ExternalEffect, error) {
	j.mu.Lock()
	eff, err := j.loadLocked(invocationID)
	j.mu.Unlock()
	if err != nil {
		return ExternalEffect{}, err
	}
	if eff.State == EffectCommitted {
		return eff, nil
	}
	if lookup == nil {
		eff.State = EffectNeedsAttention
		eff.Reason = "no remote lookup available to verify external effect"
		eff.UpdatedAt = time.Now().UnixNano()
		j.mu.Lock()
		_ = j.saveLocked(eff)
		j.mu.Unlock()
		return eff, nil
	}
	found, ref, err := lookup(ctx, eff.IdempotencyKey)
	j.mu.Lock()
	defer j.mu.Unlock()
	if err != nil || !found {
		eff.State = EffectNeedsAttention
		if err != nil {
			eff.Reason = fmt.Sprintf("reconciliation uncertain: %v", err)
		} else {
			eff.Reason = "external effect status could not be confirmed remotely"
		}
		eff.UpdatedAt = time.Now().UnixNano()
		_ = j.saveLocked(eff)
		return eff, nil
	}
	eff.State = EffectCommitted
	eff.RemoteRef = ref
	eff.Reason = ""
	eff.UpdatedAt = time.Now().UnixNano()
	return eff, j.saveLocked(eff)
}

// Pending returns all effects still in EffectPending.
func (j *EffectJournal) Pending() ([]ExternalEffect, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []ExternalEffect
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(j.dir, e.Name()))
		if err != nil {
			continue
		}
		var eff ExternalEffect
		if json.Unmarshal(data, &eff) == nil && eff.State == EffectPending {
			out = append(out, eff)
		}
	}
	return out, nil
}

func (j *EffectJournal) loadLocked(invocationID string) (ExternalEffect, error) {
	data, err := os.ReadFile(filepath.Join(j.dir, invocationID+".json"))
	if err != nil {
		return ExternalEffect{}, err
	}
	var eff ExternalEffect
	if err := json.Unmarshal(data, &eff); err != nil {
		return ExternalEffect{}, err
	}
	return eff, nil
}

func (j *EffectJournal) saveLocked(eff ExternalEffect) error {
	data, err := json.MarshalIndent(eff, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(j.dir, eff.InvocationID+".json"), data, 0600)
}
