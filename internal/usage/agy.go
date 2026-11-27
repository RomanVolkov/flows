package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

// AgyTelemetryFixture represents structured agy usage telemetry.
// Missing token fields are recorded explicitly as CoverageUnavailable.
type AgyTelemetryFixture struct {
	ConversationID string `json:"conversation_id"`
	CWD            string `json:"cwd"`
	Model          string `json:"model"`
	Timestamp      string `json:"timestamp"`
	InputTokens    *int64 `json:"input_tokens"`
	CacheTokens    *int64 `json:"cache_tokens"`
	OutputTokens   *int64 `json:"output_tokens"`
	Reasoning      *int64 `json:"reasoning_tokens"`
}

// CollectAgyFile ingests structured agy telemetry or records CoverageUnavailable
// when fields are missing (never scraping terminal cells).
func CollectAgyFile(ctx context.Context, st *store.Store, path string, candidates []SessionBinding) (Receipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	var fix AgyTelemetryFixture
	if err := json.Unmarshal(data, &fix); err != nil {
		return Receipt{}, err
	}
	evTime := parseEventTime(fix.Timestamp)
	bound, ok, ambig := AttributeSession(fix.ConversationID, fix.CWD, evTime, candidates)

	cov := CoverageUnavailable
	var inTok, cacheTok, outTok, reasonTok int64
	if fix.InputTokens != nil && fix.OutputTokens != nil {
		cov = CoverageFinal
		if ok && bound.Coverage != "" {
			cov = bound.Coverage
		}
		inTok = *fix.InputTokens
		outTok = *fix.OutputTokens
		if fix.CacheTokens != nil {
			cacheTok = *fix.CacheTokens
		}
		if fix.Reasoning != nil {
			reasonTok = *fix.Reasoning
		}
	}

	rcp := Receipt{
		ID:              "rcp-" + newHex(8),
		Provider:        "agy",
		Model:           fix.Model,
		ConversationID:  fix.ConversationID,
		SourceKey:       fmt.Sprintf("agy:%s", path),
		SessionID:       bound.SessionID,
		AttemptID:       bound.AttemptID,
		RunID:           bound.RunID,
		NodeID:          bound.NodeID,
		EventTime:       evTime,
		InputTokens:     inTok,
		CacheReadTokens: cacheTok,
		OutputTokens:    outTok,
		ReasoningTokens: reasonTok,
		Coverage:        cov,
		Ambiguous:       ambig,
	}
	if err := commitReceiptsAndOffset(ctx, st, path, int64(len(data)), 0, 0, 0, 0, []Receipt{rcp}); err != nil {
		return Receipt{}, err
	}
	return rcp, nil
}

// CollectAgyTranscript reads per-invocation usage from an Antigravity CLI transcript.
// The conversation ID comes from the transcript directory, not from prompt text.
func CollectAgyTranscript(ctx context.Context, st *store.Store, path, conversation string, candidates []SessionBinding) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var receipts []Receipt
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var row struct {
			Index      *int64 `json:"step_index"`
			Type       string `json:"type"`
			Status     string `json:"status"`
			CreatedAt  string `json:"created_at"`
			Model      string `json:"model"`
			Input      *int64 `json:"input_tokens"`
			Output     *int64 `json:"output_tokens"`
			Cache      int64  `json:"cache_read_tokens"`
			CacheWrite int64  `json:"cache_write_tokens"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Index == nil ||
			row.Type != "PLANNER_RESPONSE" || row.Status != "DONE" || row.Input == nil || row.Output == nil {
			continue
		}
		eventTime, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil || *row.Input < 0 || *row.Output < 0 || row.Cache < 0 || row.CacheWrite < 0 {
			continue
		}
		at := eventTime.UnixNano()
		eligible := make([]SessionBinding, 0, 1)
		for _, candidate := range candidates {
			if candidate.ConversationID == conversation && at >= candidate.StartedAt &&
				(candidate.EndedAt == 0 || at <= candidate.EndedAt) {
				eligible = append(eligible, candidate)
			}
		}
		receipt := Receipt{
			ID: "rcp-" + newHex(8), Provider: "agy", Model: row.Model, ConversationID: conversation,
			SourceKey: fmt.Sprintf("step:%s:%d:%d", conversation, *row.Index, at),
			EventTime: at, InputTokens: *row.Input, OutputTokens: *row.Output, CacheReadTokens: row.Cache,
			CacheWriteTokens: row.CacheWrite, ContextTokens: *row.Input + row.Cache + row.CacheWrite,
			Coverage: CoverageFinal, Ambiguous: len(eligible) > 1,
		}
		if len(eligible) == 1 {
			binding := eligible[0]
			receipt.SessionID, receipt.AttemptID, receipt.RunID, receipt.NodeID = binding.SessionID, binding.AttemptID, binding.RunID, binding.NodeID
			receipt.Coverage = binding.Coverage
		}
		receipts = append(receipts, receipt)
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if len(receipts) == 0 {
		return 0, nil
	}
	// Revisit these receipts so a conversation link discovered after collection
	// can attribute earlier activity. The source key makes the writes idempotent.
	if err := commitReceiptsAndOffset(ctx, st, path, 0, 0, 0, 0, 0, receipts); err != nil {
		return 0, err
	}
	return len(receipts), nil
}
