package github_test

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

func buildGitHubPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gh-plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/plugin")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build github plugin: %v\n%s", err, string(out))
	}
	return bin
}

func writeFakeGH(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGitHubPaginationMissingAccessChangedHeadDuplicatePRAndModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bin := buildGitHubPlugin(t)

	// 1. Multi-page discovery + duplicate PR reuse + changed head on merge.
	ghScript := `
if [ "$1" = "api" ]; then
  case "$2" in
    *"&page=1"*)
      echo '[{"id":101,"number":1,"title":"PR 1","state":"open","html_url":"https://gh/1","head":{"ref":"feat/a","sha":"sha-1"},"base":{"ref":"main"},"has_next":true}]'
      exit 0 ;;
    *"&page=2"*)
      echo '[{"id":102,"number":2,"title":"PR 2","state":"open","html_url":"https://gh/2","head":{"ref":"feat/b","sha":"sha-2"},"base":{"ref":"main"}}]'
      exit 0 ;;
    *"pulls/1"*)
      echo '{"id":101,"number":1,"head":{"ref":"feat/a","sha":"sha-NEW"}}'
      exit 0 ;;
  esac
fi
if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "SHOULD NOT CREATE DUPLICATE" >&2
  exit 1
fi
exit 0
`
	t.Setenv("FLOWS_GH_BIN", writeFakeGH(t, ghScript))

	// Direct vs node invocation both return identical 2-page PR list.
	res1, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "github.list-pull-requests", RequestID: "direct",
		Input: map[string]string{"repository": "flows/core", "state": "open"},
	})
	if err != nil {
		t.Fatalf("direct list-pull-requests: %v", err)
	}
	res2, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "github.list-pull-requests", AttemptID: "att-node", RequestID: "node",
		Input: map[string]string{"repository": "flows/core", "state": "open"},
	})
	if err != nil {
		t.Fatalf("node list-pull-requests: %v", err)
	}
	if string(res1.Output) != string(res2.Output) || !strings.Contains(string(res1.Output), "gh:102") {
		t.Fatalf("expected matching 2-page output, got direct=%s node=%s", string(res1.Output), string(res2.Output))
	}

	// Open-or-reuse PR reuses existing feat/a PR without calling `gh pr create`.
	reuseRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "github.open-or-reuse-pr",
		Input: map[string]string{"repository": "flows/core", "head": "feat/a", "base": "main", "title": "PR 1"},
	})
	if err != nil {
		t.Fatalf("open-or-reuse-pr: %v", err)
	}
	var reuseOut struct{ Reused bool }
	_ = json.Unmarshal(reuseRes.Output, &reuseOut)
	if !reuseOut.Reused {
		t.Fatalf("expected reused=true, got %s", string(reuseRes.Output))
	}

	// Changed head SHA on merge fails.
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "github.merge-pr",
		Input: map[string]any{"repository": "flows/core", "number": 1, "expected_head_sha": "sha-1"},
	}); err == nil || !strings.Contains(err.Error(), "head SHA changed") {
		t.Fatalf("expected head SHA changed error, got %v", err)
	}

	// 2. Missing access (403) surfaces authentication/access error.
	t.Setenv("FLOWS_GH_BIN", writeFakeGH(t, `echo "HTTP 403 Forbidden" >&2; exit 1`))
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "github.list-pull-requests",
		Input: map[string]string{"repository": "flows/private"},
	}); err == nil || !strings.Contains(err.Error(), "authentication/access") {
		t.Fatalf("expected auth/access error, got %v", err)
	}
}
