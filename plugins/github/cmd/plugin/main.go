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
	"regexp"
	"strings"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

// PullRequest is the normalized PR representation preserving native IDs.
type PullRequest struct {
	NativeID string `json:"native_id"`
	Number   int    `json:"number"`
	Title    string `json:"title"`
	State    string `json:"state"`
	HeadRef  string `json:"head_ref"`
	HeadSHA  string `json:"head_sha"`
	BaseRef  string `json:"base_ref"`
	URL      string `json:"url"`
}

func ghBinary() string {
	if p := os.Getenv("FLOWS_GH_BIN"); p != "" {
		return p
	}
	return "gh"
}

func runGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ghBinary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(strings.ToLower(msg), "rate limit") {
			return nil, fmt.Errorf("github rate limit exceeded: %s", msg)
		}
		if strings.Contains(strings.ToLower(msg), "403") || strings.Contains(strings.ToLower(msg), "auth") {
			return nil, fmt.Errorf("github authentication/access error: %s", msg)
		}
		return nil, fmt.Errorf("gh %s: %w (%s)", strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func normalizeRepo(repo string) (string, error) {
	value := strings.TrimSpace(repo)
	if path, ok := strings.CutPrefix(value, "git@github.com:"); ok {
		value = strings.TrimSuffix(path, ".git")
	} else if strings.Contains(value, "://") {
		remote, err := url.Parse(value)
		if err != nil || !strings.EqualFold(remote.Hostname(), "github.com") ||
			(remote.Scheme != "https" && remote.Scheme != "ssh") || remote.RawQuery != "" || remote.Fragment != "" {
			return "", fmt.Errorf("expected a GitHub repository as owner/repo or a GitHub HTTPS or SSH remote")
		}
		value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(remote.Path, "/"), "/"), ".git")
	}
	if !repoName.MatchString(value) || strings.Contains(value, "..") || strings.HasPrefix(value, "-") ||
		strings.HasSuffix(value, "/.") || strings.HasPrefix(value, "./") {
		return "", fmt.Errorf("expected a GitHub repository as owner/repo or a GitHub HTTPS or SSH remote")
	}
	return value, nil
}

func withNormalizedRepo(next sdk.Handler) sdk.Handler {
	return func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(ic.Input, &input); err != nil {
			return nil, err
		}
		var remote string
		if raw, ok := input["repository"]; ok {
			if err := json.Unmarshal(raw, &remote); err != nil {
				return nil, err
			}
		}
		repo, err := normalizeRepo(remote)
		if err != nil {
			return nil, err
		}
		input["repository"], _ = json.Marshal(repo)
		ic.Input, _ = json.Marshal(input)
		return next(ctx, ic)
	}
}

func listPRs(ctx context.Context, repo, state string) ([]PullRequest, error) {
	if state == "" {
		state = "open"
	}
	all := make([]PullRequest, 0)
	for page := 1; ; page++ {
		out, err := runGH(ctx, "api", fmt.Sprintf("repos/%s/pulls?state=%s&per_page=100&page=%d", repo, url.QueryEscape(state), page))
		if err != nil {
			return nil, err
		}
		var raw []struct {
			ID      int64  `json:"id"`
			Number  int    `json:"number"`
			Title   string `json:"title"`
			State   string `json:"state"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(out, &raw); err != nil {
			return nil, err
		}
		for _, item := range raw {
			all = append(all, PullRequest{
				NativeID: fmt.Sprintf("gh:%d", item.ID),
				Number:   item.Number,
				Title:    item.Title,
				State:    item.State,
				HeadRef:  item.Head.Ref,
				HeadSHA:  item.Head.SHA,
				BaseRef:  item.Base.Ref,
				URL:      item.HTMLURL,
			})
		}
		if len(raw) < 100 {
			// Check if gh fixture returned a next-page marker or fewer than page size.
			if !strings.Contains(string(out), `"has_next":true`) {
				break
			}
		}
	}
	return all, nil
}

func main() {
	m := sdk.Manifest{
		ID:      "github",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "github.list-pull-requests", Description: "List PRs", ReadOnly: true},
			{ID: "github.get-pull-request", Description: "Get PR details", ReadOnly: true},
			{ID: "github.open-or-reuse-pr", Description: "Open or reuse PR"},
			{ID: "github.reply-comment", Description: "Reply to PR thread"},
			{ID: "github.request-review", Description: "Request PR review"},
			{ID: "github.merge-pr", Description: "Merge PR pinned to head SHA"},
		},
	}

	handlers := map[string]sdk.Handler{
		"github.list-pull-requests": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ Repository, State string }
			if len(ic.Input) > 0 {
				if err := json.Unmarshal(ic.Input, &in); err != nil {
					return nil, err
				}
			}
			prs, err := listPRs(ctx, in.Repository, in.State)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pull_requests": prs}, nil
		},
		"github.get-pull-request": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Repository string `json:"repository"`
				Number     int    `json:"number"`
			}
			if err := json.Unmarshal(ic.Input, &in); err != nil {
				return nil, err
			}
			out, err := runGH(ctx, "api", fmt.Sprintf("repos/%s/pulls/%d", in.Repository, in.Number))
			if err != nil {
				return nil, err
			}
			var pr json.RawMessage = out
			return map[string]any{"pull_request": pr}, nil
		},
		"github.open-or-reuse-pr": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Repository string `json:"repository"`
				Head       string `json:"head"`
				Base       string `json:"base"`
				Title      string `json:"title"`
				Body       string `json:"body"`
			}
			if err := json.Unmarshal(ic.Input, &in); err != nil {
				return nil, err
			}
			existing, err := listPRs(ctx, in.Repository, "open")
			if err != nil {
				return nil, err
			}
			for _, pr := range existing {
				if pr.HeadRef == in.Head {
					return map[string]any{"reused": true, "url": pr.URL, "pull_request": pr}, nil
				}
			}
			out, err := runGH(ctx, "pr", "create", "--repo", in.Repository, "--head", in.Head, "--base", in.Base, "--title", in.Title, "--body", in.Body)
			if err != nil {
				return nil, err
			}
			return map[string]any{"reused": false, "url": strings.TrimSpace(string(out))}, nil
		},
		"github.reply-comment": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Repository string `json:"repository"`
				Number     int    `json:"number"`
				Body       string `json:"body"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			out, err := runGH(ctx, "pr", "comment", fmt.Sprintf("%d", in.Number), "--repo", in.Repository, "--body", in.Body)
			return map[string]any{"receipt": strings.TrimSpace(string(out))}, err
		},
		"github.request-review": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Repository string   `json:"repository"`
				Number     int      `json:"number"`
				Reviewers  []string `json:"reviewers"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			_, err := runGH(ctx, "pr", "edit", fmt.Sprintf("%d", in.Number), "--repo", in.Repository, "--add-reviewer", strings.Join(in.Reviewers, ","))
			return map[string]any{"requested": true}, err
		},
		"github.merge-pr": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Repository      string `json:"repository"`
				Number          int    `json:"number"`
				ExpectedHeadSHA string `json:"expected_head_sha"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			prOut, err := runGH(ctx, "api", fmt.Sprintf("repos/%s/pulls/%d", in.Repository, in.Number))
			if err != nil {
				return nil, err
			}
			var pr struct {
				Head struct {
					SHA string `json:"sha"`
				} `json:"head"`
			}
			if err := json.Unmarshal(prOut, &pr); err != nil {
				return nil, err
			}
			if in.ExpectedHeadSHA != "" && pr.Head.SHA != in.ExpectedHeadSHA {
				return nil, errors.New("head SHA changed before merge; newer revision needs review")
			}
			out, err := runGH(ctx, "pr", "merge", fmt.Sprintf("%d", in.Number), "--repo", in.Repository, "--match-head-commit", in.ExpectedHeadSHA)
			return map[string]any{"merged": true, "receipt": strings.TrimSpace(string(out))}, err
		},
	}
	for operation, handler := range handlers {
		handlers[operation] = withNormalizedRepo(handler)
	}
	if err := sdk.Serve(m, handlers); err != nil {
		os.Exit(1)
	}
}
