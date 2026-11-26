package gitlab_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/plugin"
)

func buildGitLabPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "glab-plugin")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/plugin")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gitlab plugin: %v\n%s", err, string(out))
	}
	return bin
}

func writeFakeGLab(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "glab")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGitLabPaginationEmptySearchDuplicateWriteAndModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bin := buildGitLabPlugin(t)

	// 1. Multi-page discovery, empty search, duplicate MR reuse, and merge guards.
	glabScript := `
if [ "$1" = "api" ]; then
  case "$2" in
    *"empty"*)
      echo '[]'
      exit 0 ;;
    *"&page=1"*)
      echo '[{"id":201,"iid":11,"title":"MR 11","state":"opened","web_url":"https://gl/11","source_branch":"feat/x","target_branch":"main","sha":"sha-11","blocking_discussions_resolved":true,"head_pipeline":{"status":"success"},"has_next":true}]'
      exit 0 ;;
    *"&page=2"*)
      echo '[{"id":202,"iid":12,"title":"MR 12","state":"opened","web_url":"https://gl/12","source_branch":"feat/y","target_branch":"main","sha":"sha-12","blocking_discussions_resolved":false}]'
      exit 0 ;;
    *"merge_requests/12"*)
      echo '{"id":202,"iid":12,"sha":"sha-12","blocking_discussions_resolved":false}'
      exit 0 ;;
  esac
fi
if [ "$1" = "mr" ] && [ "$2" = "create" ]; then
  echo "SHOULD NOT CREATE DUPLICATE MR" >&2
  exit 1
fi
exit 0
`
	t.Setenv("FLOWS_GLAB_BIN", writeFakeGLab(t, glabScript))

	// Direct vs scheduled node invocation return matching typed results.
	resDirect, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "gitlab.list-merge-requests", RequestID: "direct",
		Input: map[string]string{"project": "group/repo", "state": "opened"},
	})
	if err != nil {
		t.Fatalf("direct list-merge-requests: %v", err)
	}
	resNode, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "gitlab.list-merge-requests", RequestID: "sched-node", RunID: "run-gl-1", AttemptID: "att-gl-1",
		Input: map[string]string{"project": "group/repo", "state": "opened"},
	})
	if err != nil {
		t.Fatalf("node list-merge-requests: %v", err)
	}
	if string(resDirect.Output) != string(resNode.Output) {
		t.Fatalf("direct vs node output mismatch:\ndirect=%s\nnode=%s", resDirect.Output, resNode.Output)
	}
	var out struct {
		PullRequests  []map[string]any `json:"pull_requests"`
		MergeRequests []map[string]any `json:"merge_requests"`
	}
	if err := json.Unmarshal(resDirect.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.MergeRequests) != 2 || len(out.PullRequests) != 2 {
		t.Fatalf("expected 2 MRs across 2 pages, got %d", len(out.MergeRequests))
	}

	// Empty search.
	emptyRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "gitlab.list-merge-requests",
		Input: map[string]string{"project": "group/empty", "state": "opened"},
	})
	if err != nil {
		t.Fatalf("empty search: %v", err)
	}
	if !strings.Contains(string(emptyRes.Output), `"merge_requests":null`) && !strings.Contains(string(emptyRes.Output), `"merge_requests":[]`) {
		t.Fatalf("expected empty merge_requests, got %s", emptyRes.Output)
	}

	// Duplicate MR creation reuses existing MR.
	reuseRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "gitlab.open-or-reuse-mr",
		Input: map[string]string{"project": "group/repo", "source_branch": "feat/x", "target_branch": "main", "title": "MR 11"},
	})
	if err != nil {
		t.Fatalf("open-or-reuse-mr: %v", err)
	}
	if !strings.Contains(string(reuseRes.Output), `"reused":true`) {
		t.Fatalf("expected reused=true, got %s", reuseRes.Output)
	}

	// Unresolved blocking discussions prevent merge.
	_, err = plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "gitlab.merge-mr",
		Input: map[string]any{"project": "group/repo", "iid": 12, "expected_head_sha": "sha-12"},
	})
	if err == nil || !strings.Contains(err.Error(), "blocking discussions") {
		t.Fatalf("expected blocking discussions error, got %v", err)
	}
}
