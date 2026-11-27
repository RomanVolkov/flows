package usage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/usage"
)

func TestClaudeDedupCodexResetsAgyUnavailableAndAmbiguity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UnixNano()
	sharedDir := "/tmp/shared-repo"
	candidates := []usage.SessionBinding{
		{SessionID: "s1", AttemptID: "a1", RunID: "r1", NodeID: "n1", ConversationID: "conv-1", WorkDir: sharedDir, StartedAt: now - 1000, Coverage: usage.CoverageLive},
		{SessionID: "s2", AttemptID: "a2", RunID: "r2", NodeID: "n2", ConversationID: "conv-2", WorkDir: sharedDir, StartedAt: now - 1000, Coverage: usage.CoverageLive},
	}

	// 1. Claude JSONL with duplicate message ID (subagent + main / streaming update) and model switch.
	claudePath := filepath.Join(t.TempDir(), "claude.jsonl")
	claudeData := "" +
		`{"sessionId":"conv-1","cwd":"/tmp/shared-repo","message":{"id":"m1","model":"claude-sonnet-4","usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":20,"output_tokens":5}}}` + "\n" +
		`{"sessionId":"conv-1","cwd":"/tmp/shared-repo","message":{"id":"m1","model":"claude-sonnet-4","usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":20,"output_tokens":30}}}` + "\n" +
		`{"sessionId":"conv-1","cwd":"/tmp/shared-repo","message":{"id":"m2","model":"claude-opus-4","usage":{"input_tokens":200,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":40}}}` + "\n"
	if err := os.WriteFile(claudePath, []byte(claudeData), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.CollectClaudeFile(ctx, st, claudePath, candidates); err != nil {
		t.Fatal(err)
	}
	// Backfill/restart re-run produces 0 new receipts.
	if n, err := usage.CollectClaudeFile(ctx, st, claudePath, candidates); err != nil || n != 0 {
		t.Fatalf("expected 0 new receipts on restart backfill, got %d (err=%v)", n, err)
	}

	var claudeCount int
	var claudeOut int
	_ = st.DB().QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(output_tokens),0) FROM usage_receipts WHERE provider='claude'").Scan(&claudeCount, &claudeOut)
	if claudeCount != 2 || claudeOut != 70 {
		t.Fatalf("expected 2 deduplicated claude receipts with output=70, got count=%d out=%d", claudeCount, claudeOut)
	}

	// 2. Codex JSONL: cumulative differencing + counter reset + ambiguous cwd when conversation ID empty.
	codexPath := filepath.Join(t.TempDir(), "codex.jsonl")
	codexData := "" +
		`{"type":"token_count","session_id":"conv-2","cwd":"/tmp/shared-repo","model":"gpt-5-codex","total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":40,"reasoning_output_tokens":10,"context_tokens":100,"model_context_window":200000}}` + "\n" +
		`{"type":"token_count","session_id":"conv-2","cwd":"/tmp/shared-repo","model":"gpt-5-codex","total_token_usage":{"input_tokens":150,"cached_input_tokens":30,"output_tokens":60,"reasoning_output_tokens":15,"context_tokens":150,"model_context_window":200000}}` + "\n" +
		`{"type":"token_count","session_id":"","cwd":"/tmp/shared-repo","model":"gpt-5-codex","total_token_usage":{"input_tokens":25,"cached_input_tokens":5,"output_tokens":12,"reasoning_output_tokens":2,"context_tokens":25,"model_context_window":200000}}` + "\n"
	if err := os.WriteFile(codexPath, []byte(codexData), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.CollectCodexFile(ctx, st, codexPath, candidates); err != nil {
		t.Fatal(err)
	}

	var sumIn, sumOut, sumReason, ambigCount int
	_ = st.DB().QueryRowContext(ctx, "SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(ambiguous),0) FROM usage_receipts WHERE provider='codex'").Scan(&sumIn, &sumOut, &sumReason, &ambigCount)
	// Line 1: uncached 80, out 40; Line 2: uncached (50-10)=40, out 20; Line 3 (reset, no convID -> ambiguous): uncached 20, out 12.
	if sumIn != 140 || sumOut != 72 || sumReason != 17 || ambigCount != 1 {
		t.Fatalf("unexpected codex sums: in=%d out=%d reason=%d ambig=%d", sumIn, sumOut, sumReason, ambigCount)
	}

	// 3. Nested real Codex JSONL format (`session_meta`, `turn_context`, `event_msg`/`token_count`),
	// including incremental append (`offset > 0`) retaining file-level session/cwd/model and `last_token_usage` context.
	nestedCodexPath := filepath.Join(t.TempDir(), "nested-codex.jsonl")
	nestedPart1 := "" +
		`{"timestamp":"2026-11-27T10:19:36.895Z","type":"session_meta","payload":{"id":"conv-nested","cwd":"/tmp/shared-repo","timestamp":"2026-11-27T10:19:36.800Z"}}` + "\n" +
		`{"timestamp":"2026-11-27T10:19:38.095Z","type":"turn_context","payload":{"cwd":"/tmp/shared-repo","model":"gpt-6.2-sol"}}` + "\n" +
		`{"timestamp":"2026-11-27T10:19:39.842Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":200,"cached_input_tokens":50,"cache_write_input_tokens":0,"output_tokens":25,"reasoning_output_tokens":5},"last_token_usage":{"input_tokens":120},"model_context_window":272000}}}` + "\n"
	if err := os.WriteFile(nestedCodexPath, []byte(nestedPart1), 0600); err != nil {
		t.Fatal(err)
	}
	nestedBindings := []usage.SessionBinding{
		{SessionID: "s3", AttemptID: "a3", RunID: "r3", NodeID: "n3", ConversationID: "conv-nested", WorkDir: sharedDir, StartedAt: now - int64(time.Hour), Coverage: usage.CoverageLive},
	}
	if n, err := usage.CollectCodexFile(ctx, st, nestedCodexPath, nestedBindings); err != nil || n != 1 {
		t.Fatalf("expected 1 nested codex receipt, got %d (err=%v)", n, err)
	}
	nestedPart2 := nestedPart1 +
		`{"timestamp":"2026-11-27T10:20:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":320,"cached_input_tokens":80,"cache_write_input_tokens":0,"output_tokens":55,"reasoning_output_tokens":15},"last_token_usage":{"input_tokens":180},"model_context_window":272000}}}` + "\n"
	if err := os.WriteFile(nestedCodexPath, []byte(nestedPart2), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := usage.CollectCodexFile(ctx, st, nestedCodexPath, nestedBindings); err != nil || n != 1 {
		t.Fatalf("expected 1 incremental nested codex receipt, got %d (err=%v)", n, err)
	}
	nestedSum, err := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{SessionID: "s3"})
	if err != nil {
		t.Fatal(err)
	}
	// Total uncached: (200-50) + ((320-200)-(80-50)) = 150 + 90 = 240; cached=80; out=55; reason=15; context=180.
	if nestedSum.UncachedInputTokens != 240 || nestedSum.CacheReadTokens != 80 || nestedSum.OutputTokens != 55 || nestedSum.ReasoningTokens != 15 || nestedSum.LatestContextTokens != 180 || len(nestedSum.ByModel) != 1 || nestedSum.ByModel[0].Key != "gpt-6.2-sol" {
		t.Fatalf("unexpected nested codex aggregate: %+v", nestedSum)
	}

	// 4. Agy missing token fields -> CoverageUnavailable.
	agyPath := filepath.Join(t.TempDir(), "agy.json")
	_ = os.WriteFile(agyPath, []byte(`{"conversation_id":"conv-1","model":"gemini-3-pro"}`), 0600)
	rcp, err := usage.CollectAgyFile(ctx, st, agyPath, candidates)
	if err != nil || rcp.Coverage != usage.CoverageUnavailable {
		t.Fatalf("expected CoverageUnavailable for agy missing fields, got %+v err=%v", rcp, err)
	}
}

func TestAgyTranscriptAttributionAndDeduplication(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Native CLI transcript rows report uncached input and cache reads separately.
	transcript := `{"type":"USER_INPUT","status":"DONE","step_index":0,"created_at":"2026-11-27T12:00:00Z"}
{"type":"PLANNER_RESPONSE","status":"DONE","step_index":1,"created_at":"2026-11-27T12:00:01Z","model":"gemini","input_tokens":12,"cache_read_tokens":60,"output_tokens":9}
{"type":"PLANNER_RESPONSE","status":"DONE","step_index":2,"created_at":"2026-11-27T12:00:03Z","model":"gemini","input_tokens":4,"output_tokens":7}
`
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	const conversation = "agy-conversation"
	if _, err := usage.CollectAgyTranscript(t.Context(), st, path, conversation, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	bindings := []usage.SessionBinding{
		{ConversationID: conversation, SessionID: "one", RunID: "one", StartedAt: start.UnixNano(), EndedAt: start.Add(2 * time.Second).UnixNano(), Coverage: usage.CoverageFinal},
		{ConversationID: conversation, SessionID: "two", RunID: "two", StartedAt: start.Add(2 * time.Second).UnixNano(), Coverage: usage.CoverageLive},
	}
	chunk := filepath.Join(dir, "chunk.jsonl")
	if err := os.WriteFile(chunk, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, chunk, path} {
		if _, err := usage.CollectAgyTranscript(t.Context(), st, file, conversation, bindings); err != nil {
			t.Fatal(err)
		}
	}
	total, err := usage.Aggregate(t.Context(), st.DB(), usage.QueryFilter{Harness: "agy"})
	if err != nil || total.Requests != 2 || total.UncachedInputTokens != 16 || total.CacheReadTokens != 60 || total.OutputTokens != 16 {
		t.Fatalf("wrong agy totals: %+v (%v)", total, err)
	}
	for id, want := range map[string]int64{"one": 81, "two": 11} {
		bound, err := usage.Aggregate(t.Context(), st.DB(), usage.QueryFilter{Harness: "agy", RunID: id})
		if err != nil || bound.KnownSubtotal != want || bound.Requests != 1 {
			t.Fatalf("wrong usage for %s: %+v (%v)", id, bound, err)
		}
	}
}
