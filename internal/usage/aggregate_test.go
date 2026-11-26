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

func TestAggregateTimezoneSubtreeFinalizeReasoningAndRetainedAfterRemoval(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runID, _ := st.CreateRun(ctx, nil)
	parent, _ := st.AddNode(ctx, store.NewNode{RunID: runID, Kind: "group", Name: "g"})
	child, _ := st.AddNode(ctx, store.NewNode{RunID: runID, ParentID: &parent.ID, Kind: "session", Name: "s"})

	// 2026-06-01T02:00:00Z is 2026-06-01 in UTC, but 2026-05-31 22:00 in America/New_York!
	ts := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC).Format(time.RFC3339)
	codexPath := filepath.Join(t.TempDir(), "codex-tz.jsonl")
	_ = os.WriteFile(codexPath, []byte(`{"type":"token_count","session_id":"conv-tz","timestamp":"`+ts+`","model":"gpt-5-codex","total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":50,"reasoning_output_tokens":30,"context_tokens":100,"model_context_window":200000}}`+"\n"), 0600)

	_, err = usage.CollectCodexFile(ctx, st, codexPath, []usage.SessionBinding{
		{SessionID: "sess-tz", AttemptID: "att-tz", RunID: runID, NodeID: child.ID, ConversationID: "conv-tz", Coverage: usage.CoverageLive},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Midnight/timezone change check.
	nyDays, err := usage.AggregateDaily(ctx, st.DB(), usage.QueryFilter{Timezone: "America/New_York"})
	if err != nil || len(nyDays) != 1 || nyDays[0].Date != "2026-05-31" {
		t.Fatalf("expected 2026-05-31 in America/New_York, got %+v err=%v", nyDays, err)
	}
	utcDays, err := usage.AggregateDaily(ctx, st.DB(), usage.QueryFilter{Timezone: "UTC"})
	if err != nil || len(utcDays) != 1 || utcDays[0].Date != "2026-06-01" {
		t.Fatalf("expected 2026-06-01 in UTC, got %+v err=%v", utcDays, err)
	}

	// 2. Shared descendants deduplicated when both parent and child are selected.
	// Also verify reasoning (30) is NOT double-counted on top of output (50): 80 + 20 + 50 = 150.
	sum, err := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{NodeIDs: []string{parent.ID, child.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.KnownSubtotal != 150 || sum.ReasoningTokens != 30 || sum.Coverage != "live" {
		t.Fatalf("unexpected subtree/reasoning summary: %+v", sum)
	}

	// 3. Finalize session supersedes live counts without duplication.
	if err := usage.FinalizeSession(ctx, st, "sess-tz"); err != nil {
		t.Fatal(err)
	}
	sumFinal, err := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{SessionID: "sess-tz"})
	if err != nil || sumFinal.Coverage != "final" || sumFinal.KnownSubtotal != 150 {
		t.Fatalf("expected final coverage with same subtotal 150, got %+v err=%v", sumFinal, err)
	}

	// 4. Usage facts retained even after graph nodes are removed.
	_, _ = st.DB().ExecContext(ctx, "DELETE FROM nodes WHERE id=?", child.ID)
	retained, err := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{RunID: runID})
	if err != nil || retained.KnownSubtotal != 150 {
		t.Fatalf("expected retained usage 150 after node removal, got %+v err=%v", retained, err)
	}
}
