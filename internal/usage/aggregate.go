package usage

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

// QueryFilter filters /v1/usage and /v1/usage/daily aggregations.
type QueryFilter struct {
	FromUnixNano int64
	ToUnixNano   int64
	Timezone     string
	Harness      string
	Model        string
	RunID        string
	NodeIDs      []string
	SessionID    string
}

// BreakdownBucket aggregates usage for one harness or model key.
type BreakdownBucket struct {
	Key                 string `json:"key"`
	KnownSubtotal       int64  `json:"known_subtotal"`
	UncachedInputTokens int64  `json:"uncached_input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheWriteTokens    int64  `json:"cache_write_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	Requests            int64  `json:"requests"`
}

// Summary is the normalized response for /v1/usage.
type Summary struct {
	Coverage                    string            `json:"coverage"` // "live" | "final" | "partial" | "unavailable"
	KnownSubtotal               int64             `json:"known_subtotal"`
	UnavailableCount            int64             `json:"unavailable_count"`
	UncachedInputTokens         int64             `json:"uncached_input_tokens"`
	CacheReadTokens             int64             `json:"cache_read_tokens"`
	CacheWriteTokens            int64             `json:"cache_write_tokens"`
	OutputTokens                int64             `json:"output_tokens"`
	ReasoningTokens             int64             `json:"reasoning_tokens"`
	Requests                    int64             `json:"requests"`
	LatestContextTokens         int64             `json:"latest_context_tokens"`
	LatestContextWindow         int64             `json:"latest_context_window"`
	AttributedAtCompletionCount int64             `json:"attributed_at_completion_count"`
	ByHarness                   []BreakdownBucket `json:"by_harness,omitempty"`
	ByModel                     []BreakdownBucket `json:"by_model,omitempty"`
}

// DailyBucket aggregates usage for one local calendar date and model.
type DailyBucket struct {
	Date                string `json:"date"`
	Harness             string `json:"harness"`
	Model               string `json:"model"`
	KnownSubtotal       int64  `json:"known_subtotal"`
	UncachedInputTokens int64  `json:"uncached_input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheWriteTokens    int64  `json:"cache_write_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	Requests            int64  `json:"requests"`
	Coverage            string `json:"coverage"`
}

// FinalizeSession promotes live receipts for sessionID to final coverage
// without duplicating existing receipts.
func FinalizeSession(ctx context.Context, st *store.Store, sessionID string) error {
	return st.WriteTx(ctx, func(tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE usage_receipts SET coverage='final'
			WHERE session_id=? AND coverage='live'
		`, sessionID)
		return err
	})
}

// ExpandSubtree expands selected node IDs across parent_id links and deduplicates
// overlapping descendants.
func ExpandSubtree(ctx context.Context, db *sql.DB, selected []string) ([]string, error) {
	if len(selected) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(selected))
	queue := make([]string, 0, len(selected))
	for _, id := range selected {
		if id != "" && !seen[id] {
			seen[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := db.QueryContext(ctx, "SELECT id FROM nodes WHERE parent_id=?", cur)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err == nil && !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
		_ = rows.Close()
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, nil
}

// Aggregate summarizes unique usage receipts matching f.
func Aggregate(ctx context.Context, db *sql.DB, f QueryFilter) (Summary, error) {
	receipts, err := queryReceipts(ctx, db, f)
	if err != nil {
		return Summary{}, err
	}
	var sum Summary
	byH := make(map[string]*BreakdownBucket)
	byM := make(map[string]*BreakdownBucket)
	var hasLive, hasFinal, hasUnavail bool
	var latestTime int64

	for _, r := range receipts {
		sum.Requests++
		if r.AttributedAtCompletion {
			sum.AttributedAtCompletionCount++
		}
		switch r.Coverage {
		case CoverageLive:
			hasLive = true
		case CoverageFinal:
			hasFinal = true
		case CoverageUnavailable:
			hasUnavail = true
			sum.UnavailableCount++
		}
		if r.EventTime >= latestTime && r.ContextTokens > 0 {
			latestTime = r.EventTime
			sum.LatestContextTokens = r.ContextTokens
			sum.LatestContextWindow = r.ContextWindow
		}

		// KnownSubtotal sums disjoint buckets; ReasoningTokens is a subset of OutputTokens.
		sub := r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens + r.OutputTokens
		sum.KnownSubtotal += sub
		sum.UncachedInputTokens += r.InputTokens
		sum.CacheReadTokens += r.CacheReadTokens
		sum.CacheWriteTokens += r.CacheWriteTokens
		sum.OutputTokens += r.OutputTokens
		sum.ReasoningTokens += r.ReasoningTokens

		addBucket(byH, r.Provider, r, sub)
		addBucket(byM, r.Model, r, sub)
	}

	sum.Coverage = resolveCoverage(len(receipts), hasLive, hasFinal, hasUnavail)
	sum.ByHarness = sortedBuckets(byH)
	sum.ByModel = sortedBuckets(byM)
	return sum, nil
}

// AggregateDaily groups usage receipts into local-timezone calendar days by model.
func AggregateDaily(ctx context.Context, db *sql.DB, f QueryFilter) ([]DailyBucket, error) {
	loc := time.Local
	if f.Timezone != "" && f.Timezone != "Local" {
		if parsed, err := time.LoadLocation(f.Timezone); err == nil {
			loc = parsed
		}
	}
	receipts, err := queryReceipts(ctx, db, f)
	if err != nil {
		return nil, err
	}

	type key struct {
		date, harness, model string
	}
	buckets := make(map[key]*DailyBucket)
	for _, r := range receipts {
		d := time.Unix(0, r.EventTime).In(loc).Format("2006-01-02")
		k := key{date: d, harness: r.Provider, model: r.Model}
		b, ok := buckets[k]
		if !ok {
			b = &DailyBucket{Date: d, Harness: r.Provider, Model: r.Model, Coverage: string(r.Coverage)}
			buckets[k] = b
		}
		sub := r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens + r.OutputTokens
		b.KnownSubtotal += sub
		b.UncachedInputTokens += r.InputTokens
		b.CacheReadTokens += r.CacheReadTokens
		b.CacheWriteTokens += r.CacheWriteTokens
		b.OutputTokens += r.OutputTokens
		b.ReasoningTokens += r.ReasoningTokens
		b.Requests++
		if b.Coverage != string(r.Coverage) {
			b.Coverage = "partial"
		}
	}

	out := make([]DailyBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b DailyBucket) int {
		if c := strings.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		if c := strings.Compare(a.Harness, b.Harness); c != 0 {
			return c
		}
		return strings.Compare(a.Model, b.Model)
	})
	return out, nil
}

func queryReceipts(ctx context.Context, db *sql.DB, f QueryFilter) ([]Receipt, error) {
	var where []string
	var args []any

	if f.FromUnixNano > 0 {
		where = append(where, "event_time >= ?")
		args = append(args, f.FromUnixNano)
	}
	if f.ToUnixNano > 0 {
		where = append(where, "event_time <= ?")
		args = append(args, f.ToUnixNano)
	}
	if f.Harness != "" {
		where = append(where, "provider = ?")
		args = append(args, f.Harness)
	}
	if f.Model != "" {
		where = append(where, "model = ?")
		args = append(args, f.Model)
	}
	if f.RunID != "" {
		where = append(where, "run_id = ?")
		args = append(args, f.RunID)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if len(f.NodeIDs) > 0 {
		expanded, err := ExpandSubtree(ctx, db, f.NodeIDs)
		if err != nil {
			return nil, err
		}
		if len(expanded) > 0 {
			ph := strings.Repeat("?,", len(expanded))
			ph = ph[:len(ph)-1]
			where = append(where, "node_id IN ("+ph+")")
			for _, id := range expanded {
				args = append(args, id)
			}
		}
	}

	q := `SELECT id, provider, model, conversation_id, source_key, session_id, attempt_id, run_id, node_id,
		event_time, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
		reasoning_tokens, context_tokens, context_window, coverage, attributed_at_completion, ambiguous
		FROM usage_receipts`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY event_time ASC, id ASC"

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Receipt
	for rows.Next() {
		var r Receipt
		var cov string
		var attrComp, ambig int
		if err := rows.Scan(
			&r.ID, &r.Provider, &r.Model, &r.ConversationID, &r.SourceKey,
			&r.SessionID, &r.AttemptID, &r.RunID, &r.NodeID, &r.EventTime,
			&r.InputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.OutputTokens,
			&r.ReasoningTokens, &r.ContextTokens, &r.ContextWindow,
			&cov, &attrComp, &ambig,
		); err != nil {
			return nil, err
		}
		r.Coverage = Coverage(cov)
		r.AttributedAtCompletion = attrComp != 0
		r.Ambiguous = ambig != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func addBucket(m map[string]*BreakdownBucket, key string, r Receipt, sub int64) {
	if key == "" {
		key = "unknown"
	}
	b, ok := m[key]
	if !ok {
		b = &BreakdownBucket{Key: key}
		m[key] = b
	}
	b.KnownSubtotal += sub
	b.UncachedInputTokens += r.InputTokens
	b.CacheReadTokens += r.CacheReadTokens
	b.CacheWriteTokens += r.CacheWriteTokens
	b.OutputTokens += r.OutputTokens
	b.ReasoningTokens += r.ReasoningTokens
	b.Requests++
}

func sortedBuckets(m map[string]*BreakdownBucket) []BreakdownBucket {
	out := make([]BreakdownBucket, 0, len(m))
	for _, b := range m {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b BreakdownBucket) int {
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

func resolveCoverage(total int, hasLive, hasFinal, hasUnavail bool) string {
	if total == 0 || (hasUnavail && !hasLive && !hasFinal) {
		return "unavailable"
	}
	if hasUnavail {
		return "partial"
	}
	if hasLive {
		return "live"
	}
	return "final"
}
