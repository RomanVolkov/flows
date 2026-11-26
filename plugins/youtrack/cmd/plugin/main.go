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
	"strings"
	"time"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

var ytHTTP = &http.Client{Timeout: 30 * time.Second}

func ytClient() (baseURL, token string, err error) {
	baseURL = strings.TrimRight(os.Getenv("FLOWS_YOUTRACK_URL"), "/")
	token = os.Getenv("FLOWS_YOUTRACK_TOKEN")
	if baseURL == "" || token == "" {
		return "", "", errors.New("youtrack authentication required: FLOWS_YOUTRACK_URL and FLOWS_YOUTRACK_TOKEN must be set")
	}
	return baseURL, token, nil
}

func doYT(ctx context.Context, method, path string, body any) ([]byte, error) {
	base, token, err := ytClient()
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
	resp, err := ytHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("youtrack authentication/access error (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("youtrack HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func main() {
	m := sdk.Manifest{
		ID:      "youtrack",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "youtrack.get-issue", Description: "Get YouTrack issue", ReadOnly: true},
			{ID: "youtrack.transition-issue", Description: "Transition YouTrack issue"},
		},
	}
	if err := sdk.Serve(m, map[string]sdk.Handler{
		"youtrack.get-issue": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct{ ID string `json:"id"` }
			_ = json.Unmarshal(ic.Input, &in)
			raw, err := doYT(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(in.ID)+"?fields=id,idReadable,summary,customFields(name,value(name))", nil)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(raw), nil
		},
		"youtrack.transition-issue": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				ID          string `json:"id"`
				TargetState string `json:"target_state"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			raw, err := doYT(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(in.ID)+"?fields=id,idReadable,customFields(name,value(name))", nil)
			if err != nil {
				return nil, err
			}
			var issue struct {
				ID           string `json:"id"`
				IDReadable   string `json:"idReadable"`
				CustomFields []struct {
					Name  string `json:"name"`
					Value struct {
						Name string `json:"name"`
					} `json:"value"`
				} `json:"customFields"`
			}
			_ = json.Unmarshal(raw, &issue)
			for _, f := range issue.CustomFields {
				if strings.EqualFold(f.Name, "State") && strings.EqualFold(f.Value.Name, in.TargetState) {
					return map[string]any{"native_id": "yt:" + issue.ID, "id": in.ID, "state": f.Value.Name, "already_transitioned": true}, nil
				}
			}
			if strings.ContainsAny(in.TargetState, "{}\n\r;") || strings.TrimSpace(in.TargetState) == "" {
				return nil, errors.New("invalid target_state")
			}
			_, err = doYT(ctx, http.MethodPost, "/api/commands", map[string]any{
				"query":  fmt.Sprintf("State {%s}", in.TargetState),
				"issues": []map[string]string{{"idReadable": in.ID}},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"native_id": "yt:" + issue.ID, "id": in.ID, "state": in.TargetState, "already_transitioned": false}, nil
		},
	}); err != nil {
		os.Exit(1)
	}
}
