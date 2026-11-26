package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// CallbackClient gives an executing plugin run-scoped access to notifications,
// questions, artifact writes, and authorized graph edits.
type CallbackClient struct {
	attemptID string
	runID     string
	token     string
	http      *http.Client
}

// NewCallbackClient creates a run-scoped callback client over socketPath.
func NewCallbackClient(socketPath, attemptID, runID, token string) *CallbackClient {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return &CallbackClient{
		attemptID: attemptID,
		runID:     runID,
		token:     token,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
			Timeout: 10 * time.Second,
		},
	}
}

// Notify sends a user notification from the executing attempt.
func (c *CallbackClient) Notify(ctx context.Context, title, body string) error {
	return c.post(ctx, "/v1/attempts/"+c.attemptID+"/notify", map[string]string{
		"run_id": c.runID,
		"title":  title,
		"body":   body,
	}, nil)
}

// AskQuestion records a human question and returns the question ID.
func (c *CallbackClient) AskQuestion(ctx context.Context, prompt string, options []string) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	err := c.post(ctx, "/v1/attempts/"+c.attemptID+"/questions", map[string]any{
		"run_id":  c.runID,
		"prompt":  prompt,
		"options": options,
	}, &resp)
	return resp.ID, err
}

// WriteArtifact persists an artifact under the attempt.
func (c *CallbackClient) WriteArtifact(ctx context.Context, name, kind string, data []byte) error {
	return c.post(ctx, "/v1/attempts/"+c.attemptID+"/artifacts", map[string]any{
		"name": name,
		"kind": kind,
		"data": data,
	}, nil)
}

// EditGraph submits an authorized graph edit batch against expectedRevision.
func (c *CallbackClient) EditGraph(ctx context.Context, expectedRevision int64, ops any) error {
	return c.post(ctx, "/v1/runs/"+c.runID+"/edits", map[string]any{
		"expected_revision": expectedRevision,
		"operations":        ops,
	}, nil)
}

func (c *CallbackClient) post(ctx context.Context, path string, body any, out any) error {
	if c == nil || c.http == nil {
		return fmt.Errorf("callback client not configured")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://flows.local"+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("callback %s HTTP %d: %s", path, resp.StatusCode, string(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
