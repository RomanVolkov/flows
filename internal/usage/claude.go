// Package usage collects, normalizes, and attributes structured token telemetry
// from Claude, Codex, and agy without scraping terminal cells.
package usage

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

// Coverage indicates whether token telemetry is live, final, or unavailable.
type Coverage string

const (
	CoverageLive        Coverage = "live"
	CoverageFinal       Coverage = "final"
	CoverageUnavailable Coverage = "unavailable"
)

// Receipt is a normalized, deduplicated token usage measurement.
type Receipt struct {
	ID                     string   `json:"id"`
	Provider               string   `json:"provider"`
	Model                  string   `json:"model"`
	ConversationID         string   `json:"conversation_id"`
	SourceKey              string   `json:"source_key"`
	SessionID              string   `json:"session_id"`
	AttemptID              string   `json:"attempt_id"`
	RunID                  string   `json:"run_id"`
	NodeID                 string   `json:"node_id"`
	EventTime              int64    `json:"event_time"`
	InputTokens            int64    `json:"input_tokens"`
	CacheReadTokens        int64    `json:"cache_read_tokens"`
	CacheWriteTokens       int64    `json:"cache_write_tokens"`
	OutputTokens           int64    `json:"output_tokens"`
	ReasoningTokens        int64    `json:"reasoning_tokens"` // subset of OutputTokens
	ContextTokens          int64    `json:"context_tokens"`   // gauge
	ContextWindow          int64    `json:"context_window"`   // gauge
	Coverage               Coverage `json:"coverage"`
	AttributedAtCompletion bool     `json:"attributed_at_completion"`
	Ambiguous              bool     `json:"ambiguous"`
}

// SessionBinding provides attribution metadata for matching telemetry to an attempt.
type SessionBinding struct {
	SessionID      string
	AttemptID      string
	RunID          string
	NodeID         string
	ConversationID string
	WorkDir        string
	StartedAt      int64
	EndedAt        int64
	Coverage       Coverage
}

// AttributeSession matches telemetry by exact conversationID first, falling back
// to an unambiguous (cwd, eventTime) match only when exactly one session qualifies.
func AttributeSession(convID, cwd string, eventTime int64, candidates []SessionBinding) (SessionBinding, bool, bool) {
	if convID != "" {
		for _, c := range candidates {
			if c.ConversationID == convID {
				return c, true, false
			}
		}
	}
	var matches []SessionBinding
	for _, c := range candidates {
		if cwd != "" && c.WorkDir == cwd && eventTime >= c.StartedAt && (c.EndedAt == 0 || eventTime <= c.EndedAt) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, false
	}
	if len(matches) > 1 {
		// Ambiguous attribution across concurrent sessions in the same checkout.
		return SessionBinding{}, false, true
	}
	return SessionBinding{}, false, false
}

// CollectClaudeFile incrementally reads a Claude JSONL transcript from the last
// persisted byte offset, deduplicating by message ID across main and subagents.
func CollectClaudeFile(ctx context.Context, st *store.Store, path string, candidates []SessionBinding) (int, error) {
	offset, _, _, _, _, err := loadOffset(ctx, st.DB(), path)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return 0, err
	}

	var receipts []Receipt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	bytesRead := int64(0)

	for sc.Scan() {
		raw := sc.Bytes()
		bytesRead += int64(len(raw)) + 1
		var entry struct {
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
			Message   *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					InputTokens              int64 `json:"input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					OutputTokens             int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &entry) != nil || entry.Message == nil || entry.Message.Usage == nil || entry.Message.ID == "" {
			continue
		}
		evTime := parseEventTime(entry.Timestamp)
		bound, ok, ambig := AttributeSession(entry.SessionID, entry.CWD, evTime, candidates)
		cov := CoverageLive
		if ok && bound.Coverage != "" {
			cov = bound.Coverage
		}
		u := entry.Message.Usage
		receipts = append(receipts, Receipt{
			ID:               "rcp-" + newHex(8),
			Provider:         "claude",
			Model:            entry.Message.Model,
			ConversationID:   entry.SessionID,
			SourceKey:        "msg:" + entry.Message.ID,
			SessionID:        bound.SessionID,
			AttemptID:        bound.AttemptID,
			RunID:            bound.RunID,
			NodeID:           bound.NodeID,
			EventTime:        evTime,
			InputTokens:      u.InputTokens,
			CacheReadTokens:  u.CacheReadInputTokens,
			CacheWriteTokens: u.CacheCreationInputTokens,
			OutputTokens:     u.OutputTokens,
			ContextTokens:    u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
			Coverage:         cov,
			Ambiguous:        ambig,
		})
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	newOffset := offset + bytesRead
	if err := commitReceiptsAndOffset(ctx, st, path, newOffset, 0, 0, 0, 0, receipts); err != nil {
		return 0, err
	}
	return len(receipts), nil
}

func commitReceiptsAndOffset(ctx context.Context, st *store.Store, path string, byteOff, lastIn, lastCache, lastOut, lastReason int64, receipts []Receipt) error {
	return st.WriteTx(ctx, func(tx *sql.Tx, now int64) error {
		for _, r := range receipts {
			if r.EventTime == 0 {
				r.EventTime = now
			}
			_, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO usage_receipts(
					id, provider, model, conversation_id, source_key,
					session_id, attempt_id, run_id, node_id, event_time,
					input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
					reasoning_tokens, context_tokens, context_window,
					coverage, attributed_at_completion, ambiguous, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, r.ID, r.Provider, r.Model, r.ConversationID, r.SourceKey,
				r.SessionID, r.AttemptID, r.RunID, r.NodeID, r.EventTime,
				r.InputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.OutputTokens,
				r.ReasoningTokens, r.ContextTokens, r.ContextWindow,
				string(r.Coverage), boolInt(r.AttributedAtCompletion), boolInt(r.Ambiguous), now)
			if err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO usage_offsets(source_path, byte_offset, last_input, last_cache_read, last_output, last_reasoning, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_path) DO UPDATE SET
				byte_offset=excluded.byte_offset,
				last_input=excluded.last_input,
				last_cache_read=excluded.last_cache_read,
				last_output=excluded.last_output,
				last_reasoning=excluded.last_reasoning,
				updated_at=excluded.updated_at
		`, path, byteOff, lastIn, lastCache, lastOut, lastReason, now)
		return err
	})
}

func loadOffset(ctx context.Context, db *sql.DB, path string) (byteOff, lastIn, lastCache, lastOut, lastReason int64, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT byte_offset, last_input, last_cache_read, last_output, last_reasoning
		FROM usage_offsets WHERE source_path=?
	`, path).Scan(&byteOff, &lastIn, &lastCache, &lastOut, &lastReason)
	if err == sql.ErrNoRows {
		return 0, 0, 0, 0, 0, nil
	}
	return byteOff, lastIn, lastCache, lastOut, lastReason, err
}

func parseEventTime(ts string) int64 {
	if ts == "" {
		return time.Now().UnixNano()
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UnixNano()
	}
	return time.Now().UnixNano()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
