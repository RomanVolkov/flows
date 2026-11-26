package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
		ID:               "rcp-" + newHex(8),
		Provider:         "agy",
		Model:            fix.Model,
		ConversationID:   fix.ConversationID,
		SourceKey:        fmt.Sprintf("agy:%s", path),
		SessionID:        bound.SessionID,
		AttemptID:        bound.AttemptID,
		RunID:            bound.RunID,
		NodeID:           bound.NodeID,
		EventTime:        evTime,
		InputTokens:      inTok,
		CacheReadTokens:  cacheTok,
		OutputTokens:     outTok,
		ReasoningTokens:  reasonTok,
		Coverage:         cov,
		Ambiguous:        ambig,
	}
	if err := commitReceiptsAndOffset(ctx, st, path, int64(len(data)), 0, 0, 0, 0, []Receipt{rcp}); err != nil {
		return Receipt{}, err
	}
	return rcp, nil
}
