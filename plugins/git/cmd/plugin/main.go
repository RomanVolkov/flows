package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/romanvolkov/flows/internal/workspace"
	sdk "github.com/romanvolkov/flows/sdk/go"
)

func unmarshalInput(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

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
			var in struct {
				RepoDir string `json:"repo_dir"`
			}
			if err := unmarshalInput(ic.Input, &in); err != nil {
				return nil, err
			}
			return workspace.ResolveRepo(ctx, in.RepoDir)
		},
		"git.checkout": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				RepoRoot  string `json:"repo_root"`
				TargetDir string `json:"target_dir"`
				Branch    string `json:"branch"`
				ReadOnly  bool   `json:"read_only"`
			}
			if err := unmarshalInput(ic.Input, &in); err != nil {
				return nil, err
			}
			p, err := workspace.PrepareWorktree(ctx, in.RepoRoot, in.TargetDir, in.Branch, in.ReadOnly)
			return map[string]string{"path": p}, err
		},
		"git.status": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := unmarshalInput(ic.Input, &in); err != nil {
				return nil, err
			}
			return workspace.Status(ctx, in.Path)
		},
		"git.commit": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			}
			if err := unmarshalInput(ic.Input, &in); err != nil {
				return nil, err
			}
			sha, err := workspace.Commit(ctx, in.Path, in.Message)
			return map[string]string{"head_sha": sha}, err
		},
		"git.push": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Path         string `json:"path"`
				Remote       string `json:"remote"`
				Branch       string `json:"branch"`
				ExpectedHead string `json:"expected_head"`
			}
			if err := unmarshalInput(ic.Input, &in); err != nil {
				return nil, err
			}
			return workspace.Push(ctx, in.Path, in.Remote, in.Branch, in.ExpectedHead)
		},
	})
	if err != nil {
		os.Exit(1)
	}
}
