package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/usage"
)

// UsageHandler serves GET /v1/usage and GET /v1/usage/daily.
func UsageHandler(st *store.Store, home paths.Home) http.HandlerFunc {
	var collectMu sync.Mutex
	var lastCollect time.Time
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		collectMu.Lock()
		if time.Since(lastCollect) >= 5*time.Second {
			if err := collectLocalUsage(r.Context(), st, home); err != nil {
				collectMu.Unlock()
				writeError(w, http.StatusInternalServerError, "usage_collect_failed", "could not read local harness transcripts")
				return
			}
			lastCollect = time.Now()
		}
		collectMu.Unlock()
		q := r.URL.Query()
		f := usage.QueryFilter{
			Timezone:  q.Get("tz"),
			Harness:   q.Get("harness"),
			Model:     q.Get("model"),
			RunID:     q.Get("run_id"),
			NodeIDs:   q["node_id"],
			SessionID: q.Get("session_id"),
		}
		if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
			f.FromUnixNano = v
		}
		if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
			f.ToUnixNano = v
		}

		switch r.URL.Path {
		case "/v1/usage":
			sum, err := usage.Aggregate(r.Context(), st.DB(), f)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "usage_query_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, sum)
		case "/v1/usage/daily":
			days, err := usage.AggregateDaily(r.Context(), st.DB(), f)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "usage_daily_failed", err.Error())
				return
			}
			if days == nil {
				days = []usage.DailyBucket{}
			}
			writeJSON(w, http.StatusOK, days)
		default:
			writeError(w, http.StatusNotFound, "not_found", "unknown usage endpoint")
		}
	}
}

// collectLocalUsage ingests the structured transcripts written by Claude Code
// and Codex. Offsets and dedupe keys make repeated requests incremental.
func collectLocalUsage(ctx context.Context, st *store.Store, home paths.Home) error {
	homeDir := home.Environment()["HOME"]
	if homeDir == "" {
		return errors.New("home directory is unavailable")
	}
	snapshot, err := st.Snapshot(ctx)
	if err != nil {
		return err
	}
	nodes := make(map[string]store.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	bindings := make([]usage.SessionBinding, 0, len(snapshot.Attempts))
	for _, attempt := range snapshot.Attempts {
		var intent LaunchIntent
		path := filepath.Join(home.State, "intents", "att-"+safeID(attempt.ID)+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := json.Unmarshal(data, &intent); err != nil {
			return err
		}
		node, ok := nodes[attempt.NodeID]
		if !ok || intent.WorkDir == "" {
			continue
		}
		coverage := usage.CoverageLive
		endedAt := int64(0)
		if attempt.FinishedAt != nil {
			coverage = usage.CoverageFinal
			endedAt = *attempt.FinishedAt
		}
		bindings = append(bindings, usage.SessionBinding{
			SessionID: intent.SessionID, AttemptID: attempt.ID, RunID: node.RunID, NodeID: node.ID,
			WorkDir: intent.WorkDir, StartedAt: attempt.StartedAt, EndedAt: endedAt, Coverage: coverage,
		})
	}

	collectTree := func(root string, collect func(context.Context, *store.Store, string, []usage.SessionBinding) (int, error)) error {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrNotExist) || errors.Is(walkErr, os.ErrPermission) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				return nil
			}
			if _, err := collect(ctx, st, path, bindings); err != nil {
				return err
			}
			return nil
		})
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil
		}
		return err
	}
	if err := collectTree(filepath.Join(homeDir, ".claude", "projects"), usage.CollectClaudeFile); err != nil {
		return err
	}
	return collectTree(filepath.Join(homeDir, ".codex", "sessions"), usage.CollectCodexFile)
}
