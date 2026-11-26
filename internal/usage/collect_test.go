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

	// 1. Claude JSONL with duplicate message ID (subagent + main) and model switch.
	claudePath := filepath.Join(t.TempDir(), "claude.jsonl")
	claudeData := "" +
		`{"sessionId":"conv-1","cwd":"/tmp/shared-repo","message":{"id":"m1","model":"claude-sonnet-4","usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":20,"output_tokens":30}}}` + "\n" +
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
	_ = st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_receipts WHERE provider='claude'").Scan(&claudeCount)
	if claudeCount != 2 {
		t.Fatalf("expected 2 deduplicated claude receipts, got %d", claudeCount)
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

	// 3. Agy missing token fields -> CoverageUnavailable.
	agyPath := filepath.Join(t.TempDir(), "agy.json")
	_ = os.WriteFile(agyPath, []byte(`{"conversation_id":"conv-1","model":"gemini-3-pro"}`), 0600)
	rcp, err := usage.CollectAgyFile(ctx, st, agyPath, candidates)
	if err != nil || rcp.Coverage != usage.CoverageUnavailable {
		t.Fatalf("expected CoverageUnavailable for agy missing fields, got %+v err=%v", rcp, err)
	}
}
