// Package analytics implements the top-bar token summary, breakdowns,
// daily model chart/table, and visibility-aware live refresh coalescing.
package analytics

import (
	"fmt"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/usage"
)

// RangePreset selects a time window for token analytics.
type RangePreset string

const (
	RangeToday RangePreset = "today"
	Range7D    RangePreset = "7d"
	Range30D   RangePreset = "30d"
	Range90D   RangePreset = "90d"
	RangeAll   RangePreset = "all"
)

// DisplayState distinguishes zero, partial, unavailable, and complete totals.
type DisplayState string

const (
	StateZero        DisplayState = "zero"
	StatePartial     DisplayState = "partial"
	StateUnavailable DisplayState = "unavailable"
	StateKnown       DisplayState = "known"
)

// ClassifySummary determines the distinct DisplayState for a usage.Summary.
func ClassifySummary(s usage.Summary) DisplayState {
	if s.Requests == 0 {
		return StateZero
	}
	if s.Coverage == "unavailable" {
		return StateUnavailable
	}
	if s.Coverage == "partial" || s.UnavailableCount > 0 {
		return StatePartial
	}
	return StateKnown
}

// FormatTopBarTotal formats today's token total with explicit state distinction.
func FormatTopBarTotal(s usage.Summary) string {
	switch ClassifySummary(s) {
	case StateZero:
		return "0 tok"
	case StateUnavailable:
		return "n/a"
	case StatePartial:
		return fmt.Sprintf("%d+ tok (partial)", s.KnownSubtotal)
	default:
		return fmt.Sprintf("%d tok", s.KnownSubtotal)
	}
}

// OverviewModel drives the top-bar badge, range breakdowns, and live refresh coalescing.
type OverviewModel struct {
	mu            sync.Mutex
	Visible       bool
	PollingActive bool
	PendingDirty  bool
	RefreshCount  int
	LastRefresh   time.Time
	CoalesceWait  time.Duration

	Today  usage.Summary
	Ranges map[RangePreset]usage.Summary
}

// NewOverviewModel creates an OverviewModel with default 150ms coalescing.
func NewOverviewModel() *OverviewModel {
	return &OverviewModel{
		CoalesceWait: 150 * time.Millisecond,
		Ranges:       make(map[RangePreset]usage.Summary),
	}
}

// SetVisible toggles view visibility, stopping polling immediately when hidden.
func (m *OverviewModel) SetVisible(visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Visible = visible
	m.PollingActive = visible
}

// NotifyUsageEvent records a rapid usage update for coalesced refresh when visible.
func (m *OverviewModel) NotifyUsageEvent(now time.Time) (refreshed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Visible {
		m.PendingDirty = true
		return false
	}
	if !m.LastRefresh.IsZero() && now.Sub(m.LastRefresh) < m.CoalesceWait {
		m.PendingDirty = true
		return false
	}
	m.PendingDirty = false
	m.LastRefresh = now
	m.RefreshCount++
	return true
}

// FlushCoalesced performs a single refresh if rapid events were coalesced.
func (m *OverviewModel) FlushCoalesced(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Visible || !m.PendingDirty {
		return false
	}
	m.PendingDirty = false
	m.LastRefresh = now
	m.RefreshCount++
	return true
}
