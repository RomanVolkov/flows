package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrBranchInUse = errors.New("branch already checked out or exists")
	ErrStaleHead   = errors.New("checkout head changed; newer revision needs review")
)

// RepoIdentity describes a resolved Git repository and its default branch.
type RepoIdentity struct {
	Root          string `json:"root"`
	DefaultBranch string `json:"default_branch"`
	HeadSHA       string `json:"head_sha"`
}

// CheckoutStatus summarizes dirty state, current branch, and changed files.
type CheckoutStatus struct {
	Path         string   `json:"path"`
	Branch       string   `json:"branch"`
	HeadSHA      string   `json:"head_sha"`
	Dirty        bool     `json:"dirty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// PushResult reports a verified push and reconciled remote ref SHA.
type PushResult struct {
	Remote    string `json:"remote"`
	Branch    string `json:"branch"`
	RemoteSHA string `json:"remote_sha"`
}

// ResolveRepo resolves repository root, default branch, and HEAD SHA.
func ResolveRepo(ctx context.Context, repoDir string) (RepoIdentity, error) {
	root, err := gitOut(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return RepoIdentity{}, err
	}
	head, err := gitOut(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return RepoIdentity{}, err
	}
	branch, err := gitOut(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		branch = "main"
	}
	return RepoIdentity{Root: root, DefaultBranch: branch, HeadSHA: head}, nil
}

// PrepareWorktree creates an isolated git worktree for a planning (detached/read-only)
// or implementation (new branch) group.
func PrepareWorktree(ctx context.Context, repoRoot, targetDir, branch string, readOnly bool) (string, error) {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0700); err != nil {
		return "", err
	}
	if readOnly {
		if _, err := gitOut(ctx, repoRoot, "worktree", "add", "--detach", targetDir, "HEAD"); err != nil {
			return "", err
		}
		return targetDir, nil
	}
	// Reject duplicate branch requests if branch already exists.
	if _, err := gitOut(ctx, repoRoot, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
		return "", fmt.Errorf("%w: %s", ErrBranchInUse, branch)
	}
	if _, err := gitOut(ctx, repoRoot, "worktree", "add", "-b", branch, targetDir, "HEAD"); err != nil {
		return "", err
	}
	return targetDir, nil
}

// Status inspects branch, HEAD SHA, and uncommitted changes in checkoutDir.
func Status(ctx context.Context, checkoutDir string) (CheckoutStatus, error) {
	head, err := gitOut(ctx, checkoutDir, "rev-parse", "HEAD")
	if err != nil {
		return CheckoutStatus{}, err
	}
	branch, _ := gitOut(ctx, checkoutDir, "rev-parse", "--abbrev-ref", "HEAD")
	porcelain, err := gitOut(ctx, checkoutDir, "status", "--porcelain")
	if err != nil {
		return CheckoutStatus{}, err
	}
	var changed []string
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) != "" {
			changed = append(changed, strings.TrimSpace(line))
		}
	}
	return CheckoutStatus{
		Path:         checkoutDir,
		Branch:       branch,
		HeadSHA:      head,
		Dirty:        len(changed) > 0,
		ChangedFiles: changed,
	}, nil
}

// Commit stages all changes in checkoutDir and commits them with message.
func Commit(ctx context.Context, checkoutDir, message string) (string, error) {
	if _, err := gitOut(ctx, checkoutDir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, checkoutDir, "commit", "-m", message); err != nil {
		return "", err
	}
	return gitOut(ctx, checkoutDir, "rev-parse", "HEAD")
}

// Push verifies HEAD == expectedHeadSHA (rejecting stale heads), pushes branch
// to remote, and reconciles the remote ref SHA via ls-remote.
func Push(ctx context.Context, checkoutDir, remote, branch, expectedHeadSHA string) (PushResult, error) {
	head, err := gitOut(ctx, checkoutDir, "rev-parse", "HEAD")
	if err != nil {
		return PushResult{}, err
	}
	if expectedHeadSHA != "" && head != expectedHeadSHA {
		return PushResult{}, fmt.Errorf("%w: head %s != expected %s", ErrStaleHead, head, expectedHeadSHA)
	}
	if _, err := gitOut(ctx, checkoutDir, "push", remote, branch); err != nil {
		return PushResult{}, err
	}
	remoteSHA, err := ReconcileRemoteRef(ctx, checkoutDir, remote, branch)
	if err != nil {
		return PushResult{}, err
	}
	return PushResult{Remote: remote, Branch: branch, RemoteSHA: remoteSHA}, nil
}

// ReconcileRemoteRef queries remote for refs/heads/<branch>.
func ReconcileRemoteRef(ctx context.Context, checkoutDir, remote, branch string) (string, error) {
	out, err := gitOut(ctx, checkoutDir, "ls-remote", remote, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
