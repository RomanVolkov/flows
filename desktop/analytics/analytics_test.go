package analytics_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/desktop/analytics"
	"github.com/romanvolkov/flows/desktop/inspector"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/usage"
)

func TestChartToTableEqualityFiltersLiveToFinalKeyboardAndCoalescing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Seed Claude (live) and agy (unavailable) receipts.
	claudePath := filepath.Join(t.TempDir(), "c.jsonl")
	_ = os.WriteFile(claudePath, []byte(`{"sessionId":"s-c","timestamp":"2026-06-01T12:00:00Z","message":{"id":"m1","model":"claude-sonnet-4","usage":{"input_tokens":100,"cache_read_input_tokens":50,"output_tokens":25}}}`+"\n"), 0600)
	_, _ = usage.CollectClaudeFile(ctx, st, claudePath, []usage.SessionBinding{{SessionID: "sess-c", ConversationID: "s-c", Coverage: usage.CoverageLive}})

	agyPath := filepath.Join(t.TempDir(), "a.json")
	_ = os.WriteFile(agyPath, []byte(`{"conversation_id":"s-a","model":"gemini-3-pro","timestamp":"2026-06-01T12:05:00Z"}`), 0600)
	_, _ = usage.CollectAgyFile(ctx, st, agyPath, []usage.SessionBinding{{SessionID: "sess-a", ConversationID: "s-a"}})

	// 1. Mixed live + unavailable -> partial state distinct from zero.
	allSum, err := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if analytics.ClassifySummary(allSum) != analytics.StatePartial {
		t.Fatalf("expected StatePartial with missing agy coverage, got %s", analytics.ClassifySummary(allSum))
	}
	if analytics.ClassifySummary(usage.Summary{}) != analytics.StateZero {
		t.Fatalf("expected StateZero for empty summary")
	}

	// 2. Live run becoming final.
	if err := usage.FinalizeSession(ctx, st, "sess-c"); err != nil {
		t.Fatal(err)
	}
	cSum, _ := usage.Aggregate(ctx, st.DB(), usage.QueryFilter{SessionID: "sess-c"})
	insp := inspector.BuildUsageInspector("session", cSum)
	if insp.State != "final" || insp.Summary.KnownSubtotal != 175 {
		t.Fatalf("expected final state with 175 tokens, got %+v", insp)
	}

	// 3. Daily chart-to-table equality, per-model filter, and keyboard crosshair.
	buckets, err := usage.AggregateDaily(ctx, st.DB(), usage.QueryFilter{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	view := analytics.BuildDailyView(analytics.Range30D, buckets, "", "")
	for _, s := range view.Series {
		var tableSum int64
		for _, row := range view.Table {
			if row.Model == s.Model {
				tableSum += row.KnownSubtotal
			}
		}
		if tableSum != s.Total {
			t.Fatalf("chart-to-table mismatch for %s: chart=%d table=%d", s.Model, s.Total, tableSum)
		}
	}
	filtered := analytics.BuildDailyView(analytics.Range7D, buckets, "claude", "claude-sonnet-4")
	if len(filtered.Table) != 1 || filtered.Table[0].KnownSubtotal != 175 {
		t.Fatalf("filtered daily view mismatch: %+v", filtered)
	}
	if row := filtered.MoveCrosshair(1); row == nil || row.Model != "claude-sonnet-4" {
		t.Fatalf("keyboard crosshair failed: %+v", row)
	}

	// 4. Visibility polling stop & rapid event coalescing.
	ov := analytics.NewOverviewModel()
	ov.SetVisible(false)
	if ov.PollingActive || ov.NotifyUsageEvent(time.Now()) {
		t.Fatalf("polling/refresh must stop when hidden")
	}
	ov.SetVisible(true)
	t0 := time.Now()
	if !ov.FlushCoalesced(t0) {
		t.Fatalf("expected pending flush on becoming visible")
	}
	// Rapid events within CoalesceWait coalesce into one dirty flush.
	if ov.NotifyUsageEvent(t0.Add(10 * time.Millisecond)) {
		t.Fatalf("rapid event should be coalesced")
	}
	if !ov.FlushCoalesced(t0.Add(200 * time.Millisecond)) || ov.RefreshCount != 2 {
		t.Fatalf("expected 2 total refreshes after coalesce flush, got %d", ov.RefreshCount)
	}
}
