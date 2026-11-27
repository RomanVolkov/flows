package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/usage"
)

// UsageHandler serves GET /v1/usage and GET /v1/usage/daily.
func UsageHandler(st *store.Store, home paths.Home) http.HandlerFunc {
	var collectMu sync.Mutex
	var lastCollect time.Time
	agyScans := make(map[string]agyScan)
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		collectMu.Lock()
		if time.Since(lastCollect) >= 5*time.Second {
			if err := collectLocalUsage(r.Context(), st, home, agyScans); err != nil {
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

// collectLocalUsage ingests structured transcripts from the supported harnesses.
type agyScan struct {
	file     os.FileInfo
	bindings []usage.SessionBinding
}

func collectLocalUsage(ctx context.Context, st *store.Store, home paths.Home, agyScans map[string]agyScan) error {
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
	hostByAttempt := make(map[string]host.Record)
	hostRoot := filepath.Join(home.State, "hosts")
	hostDirs, err := os.ReadDir(hostRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, dir := range hostDirs {
		if !dir.IsDir() {
			continue
		}
		if record, err := host.LoadRecord(filepath.Join(hostRoot, dir.Name())); err == nil {
			hostByAttempt[record.AttemptID] = record
		}
	}
	agyBindings := make(map[string][]usage.SessionBinding)
	bindings := make(map[string][]usage.SessionBinding)
	for _, attempt := range snapshot.Attempts {
		var intent LaunchIntent
		path := filepath.Join(home.State, "intents", "att-"+safeID(attempt.ID)+".json")
		data, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(data, &intent); err != nil {
				return err
			}
		} else if errors.Is(err, os.ErrNotExist) {
			record, found := hostByAttempt[attempt.ID]
			if !found {
				continue
			}
			intent.SessionID, intent.WorkDir = record.SessionID, record.WorkDir
			switch record.Harness {
			case "claude", "codex", "agy":
				intent.Harness = harness.Kind(record.Harness)
			default:
				intent.Harness = harness.Kind(filepath.Base(record.Command))
			}
		} else {
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
		binding := usage.SessionBinding{
			SessionID: intent.SessionID, AttemptID: attempt.ID, RunID: node.RunID, NodeID: node.ID,
			WorkDir: intent.WorkDir, StartedAt: attempt.StartedAt, EndedAt: endedAt, Coverage: coverage,
		}
		provider := string(intent.Harness)
		bindings[provider] = append(bindings[provider], binding)
		if provider != "agy" {
			continue
		}
		markers, _ := os.ReadDir(agyUsageDir(home, attempt.ID))
		for _, marker := range markers {
			if marker.Type().IsRegular() {
				linked := binding
				linked.ConversationID = marker.Name()
				agyBindings[linked.ConversationID] = append(agyBindings[linked.ConversationID], linked)
			}
		}
	}

	collectTree := func(root, provider string, collect func(context.Context, *store.Store, string, []usage.SessionBinding) (int, error)) error {
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
			if _, err := collect(ctx, st, path, bindings[provider]); err != nil {
				return err
			}
			return nil
		})
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil
		}
		return err
	}
	if err := collectTree(filepath.Join(homeDir, ".claude", "projects"), "claude", usage.CollectClaudeFile); err != nil {
		return err
	}
	if err := collectTree(filepath.Join(homeDir, ".codex", "sessions"), "codex", usage.CollectCodexFile); err != nil {
		return err
	}
	brain := filepath.Join(homeDir, ".gemini", "antigravity-cli", "brain")
	conversations, err := os.ReadDir(brain)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, conversation := range conversations {
		if !conversation.IsDir() {
			continue
		}
		logs := filepath.Join(brain, conversation.Name(), ".system_generated", "logs")
		paths, err := filepath.Glob(filepath.Join(logs, "chunks", "transcript", "*.jsonl"))
		if err != nil {
			return err
		}
		paths = append(paths, filepath.Join(logs, "transcript.jsonl"))
		bound := agyBindings[conversation.Name()]
		for _, path := range paths {
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			last, scanned := agyScans[path]
			if scanned && os.SameFile(last.file, info) && last.file.Size() == info.Size() &&
				last.file.ModTime().Equal(info.ModTime()) && slices.Equal(last.bindings, bound) {
				continue
			}
			if _, err := usage.CollectAgyTranscript(ctx, st, path, conversation.Name(), bound); err != nil {
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
					continue
				}
				return err
			}
			agyScans[path] = agyScan{info, bound}
		}
	}
	return nil
}
