package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/plugin"
)

func buildPluginBin(t *testing.T, pkgPath, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, pkgPath)
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkgPath, err, string(out))
	}
	return bin
}

func TestTrackerAndChatProviderActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	jiraBin := buildPluginBin(t, "./plugins/jira/cmd/plugin", "jira-plugin")
	ytBin := buildPluginBin(t, "./plugins/youtrack/cmd/plugin", "yt-plugin")
	zulipBin := buildPluginBin(t, "./plugins/zulip/cmd/plugin", "zulip-plugin")

	// 1. Missing auth returns authentication error.
	t.Setenv("FLOWS_JIRA_URL", "")
	t.Setenv("FLOWS_JIRA_TOKEN", "")
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: jiraBin, Operation: "jira.search-issues",
		Input: map[string]any{"query_template": "project={{p}}", "params": map[string]string{"p": "FLOW"}},
	}); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("expected jira auth error, got %v", err)
	}

	// 2. Jira HTTP fixture: parameterized JQL search + injection guard.
	var lastJQL string
	jiraSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/search" {
			lastJQL = r.URL.Query().Get("jql")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []map[string]any{{
					"id": "1001", "key": "FLOW-1", "self": "https://jira/FLOW-1",
					"fields": map[string]any{"summary": "Ship feature", "status": map[string]any{"name": "Open"}},
				}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer jiraSrv.Close()
	t.Setenv("FLOWS_JIRA_URL", jiraSrv.URL)
	t.Setenv("FLOWS_JIRA_TOKEN", "tok-jira")

	jiraRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: jiraBin, Operation: "jira.search-issues",
		Input: map[string]any{
			"query_template": "project={{project}} AND status={{status}}",
			"params":         map[string]string{"project": "FLOW", "status": "In Progress"},
		},
	})
	if err != nil {
		t.Fatalf("jira.search-issues: %v", err)
	}
	if lastJQL != `project="FLOW" AND status="In Progress"` {
		t.Fatalf("unexpected bound JQL: %q", lastJQL)
	}
	if !strings.Contains(string(jiraRes.Output), `"FLOW-1"`) {
		t.Fatalf("expected FLOW-1 in jira output: %s", jiraRes.Output)
	}

	// 3. YouTrack HTTP fixture: transition once, then idempotent no-op when already in target state.
	ytState := "Open"
	ytTransitions := 0
	ytSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/issues/FLOW-2"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "2-42", "idReadable": "FLOW-2",
				"customFields": []map[string]any{{"name": "State", "value": map[string]any{"name": ytState}}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/commands":
			ytTransitions++
			ytState = "In Review"
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ytSrv.Close()
	t.Setenv("FLOWS_YOUTRACK_URL", ytSrv.URL)
	t.Setenv("FLOWS_YOUTRACK_TOKEN", "tok-yt")

	ytRes1, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: ytBin, Operation: "youtrack.transition-issue",
		Input: map[string]string{"id": "FLOW-2", "target_state": "In Review"},
	})
	if err != nil || !strings.Contains(string(ytRes1.Output), `"already_transitioned":false`) {
		t.Fatalf("expected first transition, got err=%v out=%s", err, ytRes1.Output)
	}
	ytRes2, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: ytBin, Operation: "youtrack.transition-issue",
		Input: map[string]string{"id": "FLOW-2", "target_state": "In Review"},
	})
	if err != nil || !strings.Contains(string(ytRes2.Output), `"already_transitioned":true`) || ytTransitions != 1 {
		t.Fatalf("expected idempotent second transition (count=1), got count=%d err=%v out=%s", ytTransitions, err, ytRes2.Output)
	}

	// 4. Zulip HTTP fixture: duplicate post prevention & idempotent topic resolution.
	type zMsg struct {
		ID      int64  `json:"id"`
		Subject string `json:"subject"`
		Content string `json:"content"`
	}
	var zMsgs []zMsg
	zPosts := 0
	zPatches := 0
	zulipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/messages":
			narrow := r.URL.Query().Get("narrow")
			var matched []zMsg
			for _, m := range zMsgs {
				if strings.Contains(narrow, `"`+m.Subject+`"`) {
					matched = append(matched, m)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": matched})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/messages":
			_ = r.ParseForm()
			zPosts++
			zMsgs = append(zMsgs, zMsg{ID: int64(zPosts), Subject: r.FormValue("topic"), Content: r.FormValue("content")})
			_ = json.NewEncoder(w).Encode(map[string]any{"id": zPosts})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v1/messages/"):
			_ = r.ParseForm()
			zPatches++
			for i := range zMsgs {
				zMsgs[i].Subject = r.FormValue("topic")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "success"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer zulipSrv.Close()
	t.Setenv("FLOWS_ZULIP_URL", zulipSrv.URL)
	t.Setenv("FLOWS_ZULIP_EMAIL", "bot@flows.local")
	t.Setenv("FLOWS_ZULIP_API_KEY", "key-zulip")

	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: zulipBin, Operation: "zulip.open-or-reuse-topic",
		Input: map[string]string{"channel": "eng", "topic": "PR-42", "content": "Review started"},
	}); err != nil {
		t.Fatal(err)
	}
	dupReply, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: zulipBin, Operation: "zulip.reply-topic",
		Input: map[string]string{"channel": "eng", "topic": "PR-42", "content": "Review started"},
	})
	if err != nil || !strings.Contains(string(dupReply.Output), `"duplicate":true`) || zPosts != 1 {
		t.Fatalf("expected duplicate reply suppressed, got posts=%d err=%v out=%s", zPosts, err, dupReply.Output)
	}
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: zulipBin, Operation: "zulip.resolve-topic",
		Input: map[string]string{"channel": "eng", "topic": "PR-42"},
	}); err != nil {
		t.Fatal(err)
	}
	res2, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: zulipBin, Operation: "zulip.resolve-topic",
		Input: map[string]string{"channel": "eng", "topic": "PR-42"},
	})
	if err != nil || !strings.Contains(string(res2.Output), `"already_resolved":true`) || zPatches != 1 {
		t.Fatalf("expected idempotent resolve (patches=1), got patches=%d err=%v out=%s", zPatches, err, res2.Output)
	}

	// 5. All three packages are discoverable in the catalog via sdk.Manifest.
	reg := catalog.NewRegistry()
	for _, bin := range []string{jiraBin, ytBin, zulipBin} {
		m, err := plugin.Describe(ctx, bin)
		if err != nil {
			t.Fatalf("describe %s: %v", bin, err)
		}
		var ops []catalog.OperationDescriptor
		for _, op := range m.Operations {
			ops = append(ops, catalog.OperationDescriptor{
				ID: op.ID, Description: op.Description, ExecutionKind: catalog.ExecPlugin, ReadOnly: op.ReadOnly,
			})
		}
		if err := reg.RegisterPackage(catalog.PackageProvenance{
			PackageID: m.ID, Namespace: m.ID, Version: m.Version, Owner: m.Owner, Origin: "user", Enabled: true, Operations: ops,
		}); err != nil {
			t.Fatalf("register %s: %v", m.ID, err)
		}
	}
	for _, want := range []string{"jira.search-issues", "youtrack.transition-issue", "zulip.resolve-topic"} {
		if _, _, err := reg.Describe(want); err != nil {
			t.Fatalf("catalog missing %s: %v", want, err)
		}
	}
}
