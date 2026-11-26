package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

// MergeRequest is the normalized PR/MR representation preserving GitLab native IDs.
type MergeRequest struct {
	NativeID              string `json:"native_id"`
	ProjectPath           string `json:"project_path"`
	IID                   int    `json:"iid"`
	Number                int    `json:"number"`
	Title                 string `json:"title"`
	State                 string `json:"state"`
	HeadRef               string `json:"head_ref"`
	HeadSHA               string `json:"head_sha"`
	BaseRef               string `json:"base_ref"`
	URL                   string `json:"url"`
	PipelineStatus        string `json:"pipeline_status,omitempty"`
	BlockingDiscussionsOK bool   `json:"blocking_discussions_resolved"`
}

func glabBinary() string {
	if p := os.Getenv("FLOWS_GLAB_BIN"); p != "" {
		return p
	}
	return "glab"
}

func runGLab(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, glabBinary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "auth") {
			return nil, fmt.Errorf("gitlab authentication/access error: %s", msg)
		}
		if strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") {
			return nil, fmt.Errorf("gitlab rate limit exceeded: %s", msg)
		}
		return nil, fmt.Errorf("glab %s: %w (%s)", strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}

type rawMR struct {
	ID                          int64  `json:"id"`
	IID                         int    `json:"iid"`
	Title                       string `json:"title"`
	State                       string `json:"state"`
	WebURL                      string `json:"web_url"`
	SourceBranch                string `json:"source_branch"`
	TargetBranch                string `json:"target_branch"`
	SHA                         string `json:"sha"`
	BlockingDiscussionsResolved bool   `json:"blocking_discussions_resolved"`
	HeadPipeline                *struct {
		Status string `json:"status"`
	} `json:"head_pipeline"`
}

func toMergeRequest(project string, item rawMR) MergeRequest {
	pipeline := ""
	if item.HeadPipeline != nil {
		pipeline = item.HeadPipeline.Status
	}
	return MergeRequest{
		NativeID:              fmt.Sprintf("gl:%s!%d", project, item.IID),
		ProjectPath:           project,
		IID:                   item.IID,
		Number:                item.IID,
		Title:                 item.Title,
		State:                 item.State,
		HeadRef:               item.SourceBranch,
		HeadSHA:               item.SHA,
		BaseRef:               item.TargetBranch,
		URL:                   item.WebURL,
		PipelineStatus:        pipeline,
		BlockingDiscussionsOK: item.BlockingDiscussionsResolved,
	}
}

func listMRs(ctx context.Context, project, state string) ([]MergeRequest, error) {
	if state == "" {
		state = "opened"
	}
	encoded := url.PathEscape(project)
	var all []MergeRequest
	for page := 1; ; page++ {
		out, err := runGLab(ctx, "api", fmt.Sprintf("projects/%s/merge_requests?state=%s&per_page=100&page=%d", encoded, url.QueryEscape(state), page))
		if err != nil {
			return nil, err
		}
		var raw []rawMR
		if err := json.Unmarshal(out, &raw); err != nil {
			return nil, err
		}
		for _, item := range raw {
			all = append(all, toMergeRequest(project, item))
		}
		if len(raw) < 100 && !strings.Contains(string(out), `"has_next":true`) {
			break
		}
	}
	return all, nil
}

func main() {
	m := sdk.Manifest{
		ID:      "gitlab",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "gitlab.list-merge-requests", Description: "List MRs", ReadOnly: true},
			{ID: "gitlab.get-merge-request", Description: "Get MR details", ReadOnly: true},
			{ID: "gitlab.open-or-reuse-mr", Description: "Open or reuse MR"},
			{ID: "gitlab.reply-discussion", Description: "Reply to MR discussion"},
			{ID: "gitlab.request-review", Description: "Assign MR reviewers"},
			{ID: "gitlab.merge-mr", Description: "Merge MR pinned to head SHA"},
		},
	}

	err := sdk.Serve(m, map[string]sdk.Handler{
		"gitlab.list-merge-requests": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project string `json:"project"`
				State   string `json:"state"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			mrs, err := listMRs(ctx, in.Project, in.State)
			if err != nil {
				return nil, err
			}
			// Publish both pull_requests (workflow-facing) and merge_requests (provider-facing).
			return map[string]any{"pull_requests": mrs, "merge_requests": mrs}, nil
		},
		"gitlab.get-merge-request": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project string `json:"project"`
				IID     int    `json:"iid"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			encoded := url.PathEscape(in.Project)
			mrOut, err := runGLab(ctx, "api", fmt.Sprintf("projects/%s/merge_requests/%d", encoded, in.IID))
			if err != nil {
				return nil, err
			}
			var item rawMR
			if err := json.Unmarshal(mrOut, &item); err != nil {
				return nil, err
			}
			return map[string]any{"merge_request": toMergeRequest(in.Project, item), "raw": json.RawMessage(mrOut)}, nil
		},
		"gitlab.open-or-reuse-mr": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project      string `json:"project"`
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				Title        string `json:"title"`
				Description  string `json:"description"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			existing, err := listMRs(ctx, in.Project, "opened")
			if err != nil {
				return nil, err
			}
			for _, mr := range existing {
				if mr.HeadRef == in.SourceBranch {
					return map[string]any{"reused": true, "url": mr.URL, "merge_request": mr, "pull_request": mr}, nil
				}
			}
			out, err := runGLab(ctx, "mr", "create", "--repo", in.Project, "--source-branch", in.SourceBranch, "--target-branch", in.TargetBranch, "--title", in.Title, "--description", in.Description, "--yes")
			if err != nil {
				return nil, err
			}
			return map[string]any{"reused": false, "url": strings.TrimSpace(string(out))}, nil
		},
		"gitlab.reply-discussion": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project string `json:"project"`
				IID     int    `json:"iid"`
				Body    string `json:"body"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			out, err := runGLab(ctx, "mr", "note", fmt.Sprintf("%d", in.IID), "--repo", in.Project, "--message", in.Body)
			return map[string]any{"receipt": strings.TrimSpace(string(out))}, err
		},
		"gitlab.request-review": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project   string   `json:"project"`
				IID       int      `json:"iid"`
				Reviewers []string `json:"reviewers"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			_, err := runGLab(ctx, "mr", "update", fmt.Sprintf("%d", in.IID), "--repo", in.Project, "--reviewer", strings.Join(in.Reviewers, ","))
			return map[string]any{"requested": true}, err
		},
		"gitlab.merge-mr": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Project                    string `json:"project"`
				IID                        int    `json:"iid"`
				ExpectedHeadSHA            string `json:"expected_head_sha"`
				MergeWhenPipelineSucceeds  bool   `json:"merge_when_pipeline_succeeds"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			encoded := url.PathEscape(in.Project)
			mrOut, err := runGLab(ctx, "api", fmt.Sprintf("projects/%s/merge_requests/%d", encoded, in.IID))
			if err != nil {
				return nil, err
			}
			var item rawMR
			if err := json.Unmarshal(mrOut, &item); err != nil {
				return nil, err
			}
			if in.ExpectedHeadSHA != "" && item.SHA != in.ExpectedHeadSHA {
				return nil, errors.New("head SHA changed before merge; newer revision needs review")
			}
			if !item.BlockingDiscussionsResolved {
				return nil, errors.New("blocking discussions remain unresolved")
			}
			args := []string{"mr", "merge", fmt.Sprintf("%d", in.IID), "--repo", in.Project, "--sha", in.ExpectedHeadSHA, "--yes"}
			if in.MergeWhenPipelineSucceeds {
				args = append(args, "--when-pipeline-succeeds")
			}
			out, err := runGLab(ctx, args...)
			return map[string]any{"merged": true, "receipt": strings.TrimSpace(string(out))}, err
		},
	})
	if err != nil {
		os.Exit(1)
	}
}
