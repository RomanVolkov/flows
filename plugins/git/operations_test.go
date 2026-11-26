package git_test

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

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestGitPluginOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "git-plugin")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/plugin")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build git plugin: %v\n%s", err, string(out))
	}

	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	repo := filepath.Join(tmp, "repo")
	runGit(t, tmp, "init", "--bare", remote)
	runGit(t, tmp, "init", repo)
	runGit(t, repo, "config", "user.email", "test@flows.local")
	runGit(t, repo, "config", "user.name", "Flows Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "remote", "add", "origin", remote)

	// 1. git.verify
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.verify", Input: map[string]string{"repo_dir": repo},
	}); err != nil {
		t.Fatalf("git.verify: %v", err)
	}

	// 2. git.checkout
	wtDir := filepath.Join(tmp, "wt-feat")
	coRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.checkout",
		Input: map[string]any{"repo_root": repo, "target_dir": wtDir, "branch": "feat/test"},
	})
	if err != nil {
		t.Fatalf("git.checkout: %v", err)
	}
	var coOut struct{ Path string `json:"path"` }
	if err := json.Unmarshal(coRes.Output, &coOut); err != nil || coOut.Path == "" {
		t.Fatalf("unexpected checkout output: %s err=%v", coRes.Output, err)
	}

	// 3. git.status (dirty) + git.commit
	if err := os.WriteFile(filepath.Join(coOut.Path, "work.txt"), []byte("change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.status", Input: map[string]string{"path": coOut.Path},
	})
	if err != nil || !strings.Contains(string(stRes.Output), `"dirty":true`) {
		t.Fatalf("expected dirty status, got err=%v out=%s", err, stRes.Output)
	}
	cmRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.commit",
		Input: map[string]string{"path": coOut.Path, "message": "add work"},
	})
	if err != nil {
		t.Fatalf("git.commit: %v", err)
	}
	var cmOut struct{ HeadSHA string `json:"head_sha"` }
	if err := json.Unmarshal(cmRes.Output, &cmOut); err != nil || cmOut.HeadSHA == "" {
		t.Fatalf("unexpected commit output: %s err=%v", cmRes.Output, err)
	}

	// 4. git.push rejects stale expected_head, succeeds with matching expected_head.
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.push",
		Input: map[string]string{"path": coOut.Path, "remote": "origin", "branch": "feat/test", "expected_head": "deadbeef"},
	}); err == nil {
		t.Fatal("expected stale expected_head push to fail")
	}
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary: bin, Operation: "git.push",
		Input: map[string]string{"path": coOut.Path, "remote": "origin", "branch": "feat/test", "expected_head": cmOut.HeadSHA},
	}); err != nil {
		t.Fatalf("git.push with matching head: %v", err)
	}
}
