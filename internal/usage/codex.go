package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/romanvolkov/flows/internal/store"
)

// CollectCodexFile incrementally reads Codex JSONL telemetry, differencing
// cumulative token counters and recognizing counter resets.
func CollectCodexFile(ctx context.Context, st *store.Store, path string, candidates []SessionBinding) (int, error) {
	offset, prevIn, prevCache, prevOut, prevReason, err := loadOffset(ctx, st.DB(), path)
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
	lineIdx := int64(0)

	for sc.Scan() {
		raw := sc.Bytes()
		lineStart := offset + bytesRead
		bytesRead += int64(len(raw)) + 1
		lineIdx++

		var entry struct {
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
			Model     string `json:"model"`
			Type      string `json:"type"`
			Total     *struct {
				InputTokens           int64 `json:"input_tokens"`
				CachedInputTokens     int64 `json:"cached_input_tokens"`
				OutputTokens          int64 `json:"output_tokens"`
				ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
				ContextTokens         int64 `json:"context_tokens"`
				ModelContextWindow    int64 `json:"model_context_window"`
			} `json:"total_token_usage"`
		}
		if json.Unmarshal(raw, &entry) != nil || entry.Type != "token_count" || entry.Total == nil {
			continue
		}

		// Difference cumulative counters; recognize resets when current < prev.
		dIn := diffCounter(entry.Total.InputTokens, prevIn)
		dCache := diffCounter(entry.Total.CachedInputTokens, prevCache)
		dOut := diffCounter(entry.Total.OutputTokens, prevOut)
		dReason := diffCounter(entry.Total.ReasoningOutputTokens, prevReason)

		prevIn = entry.Total.InputTokens
		prevCache = entry.Total.CachedInputTokens
		prevOut = entry.Total.OutputTokens
		prevReason = entry.Total.ReasoningOutputTokens

		// Normalize disjoint uncached input bucket if cached is included in input_tokens.
		uncachedIn := max(0, dIn-dCache)

		evTime := parseEventTime(entry.Timestamp)
		bound, ok, ambig := AttributeSession(entry.SessionID, entry.CWD, evTime, candidates)
		cov := CoverageLive
		if ok && bound.Coverage != "" {
			cov = bound.Coverage
		}

		receipts = append(receipts, Receipt{
			ID:               "rcp-" + newHex(8),
			Provider:         "codex",
			Model:            entry.Model,
			ConversationID:   entry.SessionID,
			SourceKey:        fmt.Sprintf("codex:%s:%d", path, lineStart),
			SessionID:        bound.SessionID,
			AttemptID:        bound.AttemptID,
			RunID:            bound.RunID,
			NodeID:           bound.NodeID,
			EventTime:        evTime,
			InputTokens:      uncachedIn,
			CacheReadTokens:  dCache,
			OutputTokens:     dOut,
			ReasoningTokens:  dReason,
			ContextTokens:    entry.Total.ContextTokens,
			ContextWindow:    entry.Total.ModelContextWindow,
			Coverage:         cov,
			Ambiguous:        ambig,
		})
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	newOffset := offset + bytesRead
	if err := commitReceiptsAndOffset(ctx, st, path, newOffset, prevIn, prevCache, prevOut, prevReason, receipts); err != nil {
		return 0, err
	}
	return len(receipts), nil
}

func diffCounter(cur, prev int64) int64 {
	if cur < prev {
		// Counter reset (e.g., process restart or new turn window).
		return cur
	}
	return cur - prev
}
