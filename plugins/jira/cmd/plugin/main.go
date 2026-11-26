package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

var jiraHTTP = &http.Client{Timeout: 30 * time.Second}
var placeholderRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

// Ticket is the workflow-facing normalized issue representation.
type Ticket struct {
	NativeID string `json:"native_id"`
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	URL      string `json:"url"`
}

func jiraClient() (baseURL, token string, err error) {
	baseURL = strings.TrimRight(os.Getenv("FLOWS_JIRA_URL"), "/")
	token = os.Getenv("FLOWS_JIRA_TOKEN")
	if baseURL == "" || token == "" {
		return "", "", errors.New("jira authentication required: FLOWS_JIRA_URL and FLOWS_JIRA_TOKEN must be set")
	}
	return baseURL, token, nil
}

func bindQuery(tmpl string, params map[string]string) (string, error) {
	var bindErr error
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		if bindErr != nil {
			return match
		}
		key := match[2 : len(match)-2]
		v, ok := params[key]
		if !ok {
			bindErr = fmt.Errorf("unbound query template parameter %q", key)
			return match
		}
		if strings.ContainsAny(v, "\"\n\r;") {
			bindErr = fmt.Errorf("invalid parameter %q value", key)
			return match
		}
		return fmt.Sprintf("%q", v)
	})
	if bindErr != nil {
		return "", bindErr
	}
	return out, nil
}

func doJira(ctx context.Context, method, path string, body any) ([]byte, error) {
	base, token, err := jiraClient()
	if err != nil {
		return nil, err
	}
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := jiraHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("jira authentication/access error (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("jira rate limit exceeded: %s", strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("jira HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func main() {
	m := sdk.Manifest{
		ID:      "jira",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "jira.search-issues", Description: "Search Jira issues with parameterized JQL", ReadOnly: true},
			{ID: "jira.get-issue", Description: "Get Jira issue", ReadOnly: true},
			{ID: "jira.transition-issue", Description: "Transition Jira issue"},
		},
	}
	if err := sdk.Serve(m, map[string]sdk.Handler{
		"jira.search-issues": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				QueryTemplate string            `json:"query_template"`
				Params        map[string]string `json:"params"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			jql, err := bindQuery(in.QueryTemplate, in.Params)
			if err != nil {
				return nil, err
			}
			raw, err := doJira(ctx, http.MethodGet, "/rest/api/3/search?jql="+url.QueryEscape(jql), nil)
			if err != nil {
				return nil, err
			}
			var res struct {
				Issues []struct {
					ID     string `json:"id"`
					Key    string `json:"key"`
					Self   string `json:"self"`
					Fields struct {
						Summary string `json:"summary"`
						Status  struct {
							Name string `json:"name"`
						} `json:"status"`
					} `json:"fields"`
				} `json:"issues"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, err
			}
			out := make([]Ticket, 0, len(res.Issues))
			for _, it := range res.Issues {
				out = append(out, Ticket{
					NativeID: "jira:" + it.ID,
					Key:      it.Key,
					Summary:  it.Fields.Summary,
					Status:   it.Fields.Status.Name,
					URL:      it.Self,
				})
			}
			return map[string]any{"jql": jql, "issues": out}, nil
		},
		"jira.get-issue": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ Key string `json:"key"` }
			_ = json.Unmarshal(ic.Input, &in)
			raw, err := doJira(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(in.Key), nil)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(raw), nil
		},
		"jira.transition-issue": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Key          string `json:"key"`
				TargetStatus string `json:"target_status"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			raw, err := doJira(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(in.Key), nil)
			if err != nil {
				return nil, err
			}
			var cur struct {
				Fields struct {
					Status struct{ Name string `json:"name"` } `json:"status"`
				} `json:"fields"`
			}
			_ = json.Unmarshal(raw, &cur)
			if strings.EqualFold(cur.Fields.Status.Name, in.TargetStatus) {
				return map[string]any{"key": in.Key, "status": cur.Fields.Status.Name, "already_transitioned": true}, nil
			}
			_, err = doJira(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(in.Key)+"/transitions", map[string]any{
				"transition": map[string]string{"name": in.TargetStatus},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": in.Key, "status": in.TargetStatus, "already_transitioned": false}, nil
		},
	}); err != nil {
		os.Exit(1)
	}
}
