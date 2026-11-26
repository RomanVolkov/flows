package workspace_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/workspace"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Flows",
		"GIT_AUTHOR_EMAIL=flows@example.com",
		"GIT_COMMITTER_NAME=Flows",
		"GIT_COMMITTER_EMAIL=flows@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func initTestRepo(t *testing.T) (remoteDir, repoDir string) {
	t.Helper()
	remoteDir = filepath.Join(t.TempDir(), "remote.git")
	repoDir = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(remoteDir, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, remoteDir, "init", "--bare")
	if err := os.MkdirAll(repoDir, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "init", "-b", "main")
	_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("init\n"), 0600)
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "main")
	return remoteDir, repoDir
}

func TestWorktreesBranchConflictDirtyRecoveryStalePushAndActiveWriterLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, repoDir := initTestRepo(t)
	identity, err := workspace.ResolveRepo(ctx, repoDir)
	if err != nil || identity.DefaultBranch != "main" {
		t.Fatalf("ResolveRepo: %+v err=%v", identity, err)
	}

	// 1. Two groups in one repo + identical branch request rejected.
	wt1, err := workspace.PrepareWorktree(ctx, repoDir, filepath.Join(t.TempDir(), "wt1"), "feat/g1", false)
	if err != nil {
		t.Fatal(err)
	}
	wt2, err := workspace.PrepareWorktree(ctx, repoDir, filepath.Join(t.TempDir(), "wt2"), "feat/g2", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.PrepareWorktree(ctx, repoDir, filepath.Join(t.TempDir(), "wt-dup"), "feat/g1", false); !errors.Is(err, workspace.ErrBranchInUse) {
		t.Fatalf("expected ErrBranchInUse, got %v", err)
	}

	// 2. Active writer blocks same-checkout retry & disposal until confirmed stopped.
	cmd := exec.Command("/bin/sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writerProc, err := host.ProbeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}

	lm := workspace.NewLeaseManager()
	ls, err := lm.Acquire("ws-1", "g1", "att-1", wt1, "feat/g1", identity.HeadSHA, &writerProc)
	if err != nil {
		t.Fatal(err)
	}
	ls.Releasable = true

	if _, err := lm.Acquire("ws-1", "g1", "att-2", wt1, "feat/g1", identity.HeadSHA, nil); !errors.Is(err, workspace.ErrCheckoutBlocked) {
		t.Fatalf("expected ErrCheckoutBlocked while writer alive, got %v", err)
	}
	if err := lm.CanDispose("ws-1"); !errors.Is(err, workspace.ErrActiveSession) {
		t.Fatalf("expected ErrActiveSession on dispose while writer alive, got %v", err)
	}

	// Kill writer; now retry & disposal become eligible.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := lm.Acquire("ws-1", "g1", "att-2", wt1, "feat/g1", identity.HeadSHA, nil); err != nil {
		t.Fatalf("expected reuse after writer stopped, got %v", err)
	}

	// 3. Recovery with uncommitted changes + commit + stale-head push rejection + reconcile.
	_ = os.WriteFile(filepath.Join(wt2, "work.txt"), []byte("deliberate change\n"), 0600)
	st, err := workspace.Status(ctx, wt2)
	if err != nil || !st.Dirty || len(st.ChangedFiles) == 0 {
		t.Fatalf("expected dirty status, got %+v err=%v", st, err)
	}
	newSHA, err := workspace.Commit(ctx, wt2, "feat: g2 work")
	if err != nil || newSHA == identity.HeadSHA {
		t.Fatalf("Commit failed: sha=%s err=%v", newSHA, err)
	}
	if _, err := workspace.Push(ctx, wt2, "origin", "feat/g2", identity.HeadSHA); !errors.Is(err, workspace.ErrStaleHead) {
		t.Fatalf("expected ErrStaleHead when pushing with old head, got %v", err)
	}
	pushRes, err := workspace.Push(ctx, wt2, "origin", "feat/g2", newSHA)
	if err != nil || pushRes.RemoteSHA != newSHA {
		t.Fatalf("Push failed: %+v err=%v", pushRes, err)
	}
}
