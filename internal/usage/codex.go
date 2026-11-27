package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/romanvolkov/flows/internal/store"
)

type codexTokenCounts struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	ContextTokens         int64 `json:"context_tokens"`
	ModelContextWindow    int64 `json:"model_context_window"`
}

type codexEnvelope struct {
	SessionID string            `json:"session_id"`
	CWD       string            `json:"cwd"`
	Timestamp string            `json:"timestamp"`
	Model     string            `json:"model"`
	Type      string            `json:"type"`
	Total     *codexTokenCounts `json:"total_token_usage"`
	Payload   *struct {
		Type              string `json:"type"`
		ID                string `json:"id"`
		SessionID         string `json:"session_id"`
		CWD               string `json:"cwd"`
		Timestamp         string `json:"timestamp"`
		Model             string `json:"model"`
		CollaborationMode *struct {
			Settings *struct {
				Model string `json:"model"`
			} `json:"settings"`
		} `json:"collaboration_mode"`
		Info *struct {
			Total  *codexTokenCounts `json:"total_token_usage"`
			Last   *codexTokenCounts `json:"last_token_usage"`
			Window int64             `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

func (e *codexEnvelope) applyMetadata(sessionID, cwd, model *string) {
	if e.SessionID != "" {
		*sessionID = e.SessionID
	}
	if e.CWD != "" {
		*cwd = e.CWD
	}
	if e.Model != "" {
		*model = e.Model
	}
	if e.Payload == nil {
		return
	}
	if e.Payload.ID != "" {
		*sessionID = e.Payload.ID
	} else if e.Payload.SessionID != "" {
		*sessionID = e.Payload.SessionID
	}
	if e.Payload.CWD != "" {
		*cwd = e.Payload.CWD
	}
	if e.Payload.Model != "" {
		*model = e.Payload.Model
	} else if e.Payload.CollaborationMode != nil && e.Payload.CollaborationMode.Settings != nil && e.Payload.CollaborationMode.Settings.Model != "" {
		*model = e.Payload.CollaborationMode.Settings.Model
	}
}

func readCodexPrefixMetadata(f *os.File, limit int64) (sessionID, cwd, model string, err error) {
	if limit <= 0 {
		return "", "", "", nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", "", "", err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	var read int64
	for read < limit {
		line, readErr := r.ReadBytes('\n')
		read += int64(len(line))
		if len(line) > 0 && (bytes.Contains(line, []byte(`"session_meta"`)) || bytes.Contains(line, []byte(`"turn_context"`))) {
			var entry codexEnvelope
			if json.Unmarshal(bytes.TrimSpace(line), &entry) == nil {
				entry.applyMetadata(&sessionID, &cwd, &model)
			}
		}
		if readErr != nil {
			break
		}
	}
	return sessionID, cwd, model, nil
}

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

	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() <= offset {
		return 0, nil
	}

	var fileSessionID, fileCWD, fileModel string
	if offset > 0 {
		fileSessionID, fileCWD, fileModel, err = readCodexPrefixMetadata(f, offset)
		if err != nil {
			return 0, err
		}
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return 0, err
	}

	var receipts []Receipt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	bytesRead := int64(0)

	for sc.Scan() {
		raw := sc.Bytes()
		lineStart := offset + bytesRead
		bytesRead += int64(len(raw)) + 1

		if !bytes.Contains(raw, []byte(`"token_count"`)) &&
			!bytes.Contains(raw, []byte(`"session_meta"`)) &&
			!bytes.Contains(raw, []byte(`"turn_context"`)) {
			continue
		}

		var entry codexEnvelope
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		entry.applyMetadata(&fileSessionID, &fileCWD, &fileModel)

		var total, last *codexTokenCounts
		var window int64
		switch {
		case entry.Type == "token_count" && entry.Total != nil:
			total = entry.Total
			window = entry.Total.ModelContextWindow
		case entry.Type == "event_msg" && entry.Payload != nil && entry.Payload.Type == "token_count" &&
			entry.Payload.Info != nil && entry.Payload.Info.Total != nil:
			total = entry.Payload.Info.Total
			last = entry.Payload.Info.Last
			window = entry.Payload.Info.Window
			if window == 0 {
				window = total.ModelContextWindow
			}
		default:
			continue
		}

		// Difference cumulative counters; recognize resets when current < prev.
		dIn := diffCounter(total.InputTokens, prevIn)
		dCache := diffCounter(total.CachedInputTokens, prevCache)
		dOut := diffCounter(total.OutputTokens, prevOut)
		dReason := diffCounter(total.ReasoningOutputTokens, prevReason)

		prevIn = total.InputTokens
		prevCache = total.CachedInputTokens
		prevOut = total.OutputTokens
		prevReason = total.ReasoningOutputTokens

		// Skip duplicate zero-delta snapshots emitted by Codex between turns.
		if dIn == 0 && dCache == 0 && dOut == 0 && dReason == 0 {
			continue
		}

		// Normalize disjoint uncached input bucket if cached is included in input_tokens.
		uncachedIn := max(0, dIn-dCache)

		ctxTokens := total.ContextTokens
		if ctxTokens == 0 && last != nil {
			ctxTokens = last.InputTokens
		}

		sessionID := entry.SessionID
		cwd := entry.CWD
		model := strings.TrimSpace(entry.Model)
		if entry.Type == "event_msg" {
			if sessionID == "" {
				sessionID = fileSessionID
			}
			if cwd == "" {
				cwd = fileCWD
			}
			if model == "" {
				model = strings.TrimSpace(fileModel)
			}
		}
		if model == "" {
			model = "codex"
		}

		evTime := parseEventTime(entry.Timestamp)
		bound, ok, ambig := AttributeSession(sessionID, cwd, evTime, candidates)
		cov := CoverageLive
		if ok && bound.Coverage != "" {
			cov = bound.Coverage
		}

		receipts = append(receipts, Receipt{
			ID:               "rcp-" + newHex(8),
			Provider:         "codex",
			Model:            model,
			ConversationID:   sessionID,
			SourceKey:        fmt.Sprintf("codex:%s:%d", path, lineStart),
			SessionID:        bound.SessionID,
			AttemptID:        bound.AttemptID,
			RunID:            bound.RunID,
			NodeID:           bound.NodeID,
			EventTime:        evTime,
			InputTokens:      uncachedIn,
			CacheReadTokens:  dCache,
			CacheWriteTokens: total.CacheWriteInputTokens,
			OutputTokens:     dOut,
			ReasoningTokens:  dReason,
			ContextTokens:    ctxTokens,
			ContextWindow:    window,
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
