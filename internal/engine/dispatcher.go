package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DispatcherConfig bounds concurrency, iterations, retries, and runtime.
type DispatcherConfig struct {
	MaxConcurrency int
	MaxIterations  int
	MaxRetries     int
	MaxRuntime     time.Duration
}

// DispatchIntent is persisted atomically when a step is claimed.
type DispatchIntent struct {
	RunID        string `json:"run_id"`
	StepID       string `json:"step_id"`
	AttemptID    string `json:"attempt_id"`
	InvocationID string `json:"invocation_id"`
	WorkspaceKey string `json:"workspace_key,omitempty"`
	ClaimedAt    int64  `json:"claimed_at"`
}

// Dispatcher schedules ready work with bounded concurrency, per-workspace locks,
// callback deduplication, and limit enforcement.
type Dispatcher struct {
	Graph      *GraphManager
	Milestones *MilestoneRegistry
	Effects    *EffectJournal
	Config     DispatcherConfig
	StateDir   string

	mu             sync.Mutex
	wsLocks        map[string]*sync.Mutex
	seenCallbacks  map[string]bool
	activeWorkers  int
	Wakeup         chan struct{}
}

// NewDispatcher creates a Dispatcher rooted at stateDir.
func NewDispatcher(stateDir string, gm *GraphManager, ms *MilestoneRegistry, ej *EffectJournal, cfg DispatcherConfig) *Dispatcher {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	return &Dispatcher{
		Graph:         gm,
		Milestones:    ms,
		Effects:       ej,
		Config:        cfg,
		StateDir:      stateDir,
		wsLocks:       make(map[string]*sync.Mutex),
		seenCallbacks: make(map[string]bool),
		Wakeup:        make(chan struct{}, 16),
	}
}

// ClaimAndRecordIntent claims stepID via GraphManager and persists a DispatchIntent
// with stable AttemptID and InvocationID before dispatching.
func (d *Dispatcher) ClaimAndRecordIntent(runID, stepID, attemptID, workspaceKey string) (DispatchIntent, error) {
	d.mu.Lock()
	if d.activeWorkers >= d.Config.MaxConcurrency {
		d.mu.Unlock()
		return DispatchIntent{}, fmt.Errorf("concurrency limit %d reached", d.Config.MaxConcurrency)
	}
	d.activeWorkers++
	d.mu.Unlock()

	if err := d.Graph.Claim(runID, stepID); err != nil {
		d.ReleaseWorker()
		return DispatchIntent{}, err
	}

	intent := DispatchIntent{
		RunID:        runID,
		StepID:       stepID,
		AttemptID:    attemptID,
		InvocationID: "inv-" + attemptID,
		WorkspaceKey: workspaceKey,
		ClaimedAt:    time.Now().UnixNano(),
	}
	if d.StateDir != "" {
		dir := filepath.Join(d.StateDir, "dispatch")
		if err := os.MkdirAll(dir, 0700); err != nil {
			d.ReleaseWorker()
			return DispatchIntent{}, err
		}
		data, _ := json.MarshalIndent(intent, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, attemptID+".json"), data, 0600); err != nil {
			d.ReleaseWorker()
			return DispatchIntent{}, err
		}
	}
	return intent, nil
}

// ReleaseWorker frees one concurrency slot and signals Wakeup.
func (d *Dispatcher) ReleaseWorker() {
	d.mu.Lock()
	if d.activeWorkers > 0 {
		d.activeWorkers--
	}
	d.mu.Unlock()
	d.notifyWakeup()
}

// AcquireWorkspaceLock serializes writes to workspaceKey.
func (d *Dispatcher) AcquireWorkspaceLock(workspaceKey string) func() {
	d.mu.Lock()
	lk, ok := d.wsLocks[workspaceKey]
	if !ok {
		lk = &sync.Mutex{}
		d.wsLocks[workspaceKey] = lk
	}
	d.mu.Unlock()
	lk.Lock()
	return lk.Unlock
}

// CompleteCallback deduplicates producer callbacks by (attemptID, eventID) and
// transitions the step + evaluates newly ready downstream steps.
func (d *Dispatcher) CompleteCallback(runID, stepID, attemptID, eventID string, succeeded bool, outputs map[string]any) (accepted bool, newlyReady []string) {
	cbKey := attemptID + ":" + eventID
	d.mu.Lock()
	if d.seenCallbacks[cbKey] {
		d.mu.Unlock()
		return false, nil
	}
	d.seenCallbacks[cbKey] = true
	d.mu.Unlock()

	d.Graph.mu.Lock()
	rg, ok := d.Graph.runs[runID]
	if !ok {
		d.Graph.mu.Unlock()
		return false, nil
	}
	st, ok := rg.Steps[stepID]
	if !ok || st.Status != StepRunning {
		d.Graph.mu.Unlock()
		return false, nil
	}
	if succeeded {
		st.Status = StepSucceeded
		st.Outputs = outputs
	} else {
		st.Status = StepFailed
	}
	newlyReady = EvaluateReadiness(rg.Steps, rg.Edges, d.Milestones)
	d.Graph.mu.Unlock()

	d.ReleaseWorker()
	return true, newlyReady
}

// EnforceLimits checks iteration, retry, and runtime limits and returns
// (exceeded, visibleReason).
func (d *Dispatcher) EnforceLimits(iterations, retries int, elapsed time.Duration) (bool, string) {
	if d.Config.MaxIterations > 0 && iterations > d.Config.MaxIterations {
		return true, fmt.Sprintf("iteration limit exceeded (%d > %d)", iterations, d.Config.MaxIterations)
	}
	if d.Config.MaxRetries > 0 && retries > d.Config.MaxRetries {
		return true, fmt.Sprintf("retry limit exceeded (%d > %d)", retries, d.Config.MaxRetries)
	}
	if d.Config.MaxRuntime > 0 && elapsed > d.Config.MaxRuntime {
		return true, fmt.Sprintf("runtime limit exceeded (%s > %s)", elapsed, d.Config.MaxRuntime)
	}
	return false, ""
}

// LoadPendingIntents reads persisted dispatch intents on restart.
func (d *Dispatcher) LoadPendingIntents() ([]DispatchIntent, error) {
	if d.StateDir == "" {
		return nil, nil
	}
	dir := filepath.Join(d.StateDir, "dispatch")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []DispatchIntent
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var in DispatchIntent
		if json.Unmarshal(data, &in) == nil && in.AttemptID != "" {
			out = append(out, in)
		}
	}
	return out, nil
}

func (d *Dispatcher) notifyWakeup() {
	select {
	case d.Wakeup <- struct{}{}:
	default:
	}
}
