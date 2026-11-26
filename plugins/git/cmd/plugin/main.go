package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/romanvolkov/flows/internal/workspace"
	sdk "github.com/romanvolkov/flows/sdk/go"
)

func main() {
	m := sdk.Manifest{
		ID:      "git",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "git.checkout", Description: "Prepare isolated worktree"},
			{ID: "git.verify", Description: "Verify repository", ReadOnly: true},
			{ID: "git.status", Description: "Inspect checkout status", ReadOnly: true},
			{ID: "git.commit", Description: "Commit checkout changes"},
			{ID: "git.push", Description: "Push verified head"},
		},
	}
	err := sdk.Serve(m, map[string]sdk.Handler{
		"git.verify": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ RepoDir string `json:"repo_dir"` }
			_ = json.Unmarshal(ic.Input, &in)
			return workspace.ResolveRepo(ctx, in.RepoDir)
		},
		"git.checkout": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				RepoRoot  string `json:"repo_root"`
				TargetDir string `json:"target_dir"`
				Branch    string `json:"branch"`
				ReadOnly  bool   `json:"read_only"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			p, err := workspace.PrepareWorktree(ctx, in.RepoRoot, in.TargetDir, in.Branch, in.ReadOnly)
			return map[string]string{"path": p}, err
		},
		"git.status": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ Path string `json:"path"` }
			_ = json.Unmarshal(ic.Input, &in)
			return workspace.Status(ctx, in.Path)
		},
		"git.commit": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ Path, Message string }
			_ = json.Unmarshal(ic.Input, &in)
			sha, err := workspace.Commit(ctx, in.Path, in.Message)
			return map[string]string{"head_sha": sha}, err
		},
		"git.push": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ Path, Remote, Branch, ExpectedHead string }
			_ = json.Unmarshal(ic.Input, &in)
			return workspace.Push(ctx, in.Path, in.Remote, in.Branch, in.ExpectedHead)
		},
	})
	if err != nil {
		os.Exit(1)
	}
}
