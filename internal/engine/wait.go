package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrObsoleteWatchPoll = errors.New("obsolete or cancelled watch poll response")

// ObservationResult is the typed output of a single read-only watch poll.
type ObservationResult struct {
	Done   bool            `json:"done"`
	Cursor string          `json:"cursor,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// WatchState is the durable engine-owned state of a watch timer.
type WatchState struct {
	WatchID          string          `json:"watch_id"`
	Ordinal          int             `json:"ordinal"`
	Cursor           string          `json:"cursor,omitempty"`
	LastObservation  json.RawMessage `json:"last_observation,omitempty"`
	NextWakeUnixNano int64           `json:"next_wake_unix_nano"`
	DeadlineUnixNano int64           `json:"deadline_unix_nano"`
	Cancelled        bool            `json:"cancelled"`
	TerminalState    string          `json:"terminal_state,omitempty"` // "" | "succeeded" | "timed_out" | "cancelled"
	WorkerSlotHeld   bool            `json:"worker_slot_held"`
}

// WatchStore persists watch timers durably so restarts resume from the exact
// cursor and absolute wake timestamp.
type WatchStore struct {
	mu      sync.Mutex
	dir     string
	watches map[string]WatchState
}

// OpenWatchStore loads any persisted watch timers from dir.
func OpenWatchStore(dir string) (*WatchStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	ws := &WatchStore{
		dir:     dir,
		watches: make(map[string]WatchState),
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var state WatchState
		if json.Unmarshal(data, &state) == nil && state.WatchID != "" {
			ws.watches[state.WatchID] = state
		}
	}
	return ws, nil
}

// StartWatch initializes a durable watch timer with an absolute deadline.
func (w *WatchStore) StartWatch(watchID string, deadline time.Duration, now time.Time) (WatchState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := WatchState{
		WatchID:          watchID,
		Ordinal:          1,
		NextWakeUnixNano: now.UnixNano(),
		DeadlineUnixNano: now.Add(deadline).UnixNano(),
		WorkerSlotHeld:   false,
	}
	if err := w.saveLocked(st); err != nil {
		return WatchState{}, err
	}
	w.watches[watchID] = st
	return st, nil
}

// RecordPoll commits observation, cursor, and next wake (or terminal state)
// atomically, releasing the worker slot and rejecting obsolete/cancelled polls.
func (w *WatchStore) RecordPoll(watchID string, ordinal int, obs ObservationResult, interval time.Duration, now time.Time) (WatchState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.watches[watchID]
	if !ok {
		return WatchState{}, fmt.Errorf("unknown watch %q", watchID)
	}
	if st.Cancelled || st.TerminalState != "" || ordinal != st.Ordinal {
		return WatchState{}, ErrObsoleteWatchPoll
	}

	st.Cursor = obs.Cursor
	st.LastObservation = append([]byte(nil), obs.Data...)
	st.WorkerSlotHeld = false

	if obs.Done {
		st.TerminalState = "succeeded"
		st.NextWakeUnixNano = 0
	} else if now.UnixNano() >= st.DeadlineUnixNano {
		st.TerminalState = "timed_out"
		st.NextWakeUnixNano = 0
	} else {
		st.Ordinal++
		st.NextWakeUnixNano = now.Add(interval).UnixNano()
	}

	if err := w.saveLocked(st); err != nil {
		return WatchState{}, err
	}
	w.watches[watchID] = st
	return st, nil
}

// Cancel marks a watch cancelled so any in-flight obsolete poll response is ignored.
func (w *WatchStore) Cancel(watchID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.watches[watchID]
	if !ok {
		return nil
	}
	st.Cancelled = true
	st.TerminalState = "cancelled"
	st.WorkerSlotHeld = false
	if err := w.saveLocked(st); err != nil {
		return err
	}
	w.watches[watchID] = st
	return nil
}

// DueWatches returns all active watches whose NextWakeUnixNano <= now.
func (w *WatchStore) DueWatches(now time.Time) []WatchState {
	w.mu.Lock()
	defer w.mu.Unlock()
	var due []WatchState
	for _, st := range w.watches {
		if !st.Cancelled && st.TerminalState == "" && st.NextWakeUnixNano <= now.UnixNano() {
			due = append(due, st)
		}
	}
	return due
}

func (w *WatchStore) saveLocked(st WatchState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, st.WatchID+".json"), data, 0600)
}
