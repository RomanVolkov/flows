package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

func zulipClient() (baseURL, email, apiKey string, err error) {
	baseURL = strings.TrimRight(os.Getenv("FLOWS_ZULIP_URL"), "/")
	email = os.Getenv("FLOWS_ZULIP_EMAIL")
	apiKey = os.Getenv("FLOWS_ZULIP_API_KEY")
	if baseURL == "" || email == "" || apiKey == "" {
		return "", "", "", errors.New("zulip authentication required: FLOWS_ZULIP_URL, FLOWS_ZULIP_EMAIL, and FLOWS_ZULIP_API_KEY must be set")
	}
	return baseURL, email, apiKey, nil
}

func doZulip(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	base, email, apiKey, err := zulipClient()
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if form != nil && method != http.MethodGet {
		body = strings.NewReader(form.Encode())
	} else if form != nil && method == http.MethodGet && len(form) > 0 {
		path += "?" + form.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(email, apiKey)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("zulip authentication/access error (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("zulip HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func topicMessages(ctx context.Context, channel, topic string) ([]struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
	Subject string `json:"subject"`
}, error) {
	narrow, _ := json.Marshal([]map[string]string{
		{"operator": "channel", "operand": channel},
		{"operator": "topic", "operand": topic},
	})
	raw, err := doZulip(ctx, http.MethodGet, "/api/v1/messages", url.Values{
		"anchor":     {"newest"},
		"num_before": {"10"},
		"num_after":  {"0"},
		"narrow":     {string(narrow)},
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Messages []struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
			Subject string `json:"subject"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Messages, nil
}

func main() {
	m := sdk.Manifest{
		ID:      "zulip",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "zulip.open-or-reuse-topic", Description: "Open or reuse Zulip topic"},
			{ID: "zulip.reply-topic", Description: "Reply to Zulip topic"},
			{ID: "zulip.resolve-topic", Description: "Resolve Zulip topic"},
		},
	}
	if err := sdk.Serve(m, map[string]sdk.Handler{
		"zulip.open-or-reuse-topic": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Channel string `json:"channel"`
				Topic   string `json:"topic"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			msgs, err := topicMessages(ctx, in.Channel, in.Topic)
			if err != nil {
				return nil, err
			}
			if len(msgs) > 0 {
				return map[string]any{"reused": true, "channel": in.Channel, "topic": in.Topic, "message_id": msgs[len(msgs)-1].ID}, nil
			}
			raw, err := doZulip(ctx, http.MethodPost, "/api/v1/messages", url.Values{
				"type":    {"stream"},
				"to":      {in.Channel},
				"topic":   {in.Topic},
				"content": {in.Content},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"reused": false, "channel": in.Channel, "topic": in.Topic, "response": json.RawMessage(raw)}, nil
		},
		"zulip.reply-topic": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Channel string `json:"channel"`
				Topic   string `json:"topic"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			msgs, err := topicMessages(ctx, in.Channel, in.Topic)
			if err != nil {
				return nil, err
			}
			for _, msg := range msgs {
				if strings.TrimSpace(msg.Content) == strings.TrimSpace(in.Content) {
					return map[string]any{"duplicate": true, "message_id": msg.ID}, nil
				}
			}
			raw, err := doZulip(ctx, http.MethodPost, "/api/v1/messages", url.Values{
				"type":    {"stream"},
				"to":      {in.Channel},
				"topic":   {in.Topic},
				"content": {in.Content},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"duplicate": false, "response": json.RawMessage(raw)}, nil
		},
		"zulip.resolve-topic": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Channel   string `json:"channel"`
				Topic     string `json:"topic"`
				MessageID int64  `json:"message_id"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			if strings.HasPrefix(in.Topic, "✔ ") {
				return map[string]any{"already_resolved": true, "topic": in.Topic}, nil
			}
			msgs, err := topicMessages(ctx, in.Channel, "✔ "+in.Topic)
			if err != nil {
				return nil, err
			}
			if len(msgs) > 0 {
				return map[string]any{"already_resolved": true, "topic": "✔ " + in.Topic}, nil
			}
			msgID := in.MessageID
			if msgID == 0 {
				openMsgs, err := topicMessages(ctx, in.Channel, in.Topic)
				if err != nil {
					return nil, err
				}
				if len(openMsgs) == 0 {
					return nil, errors.New("no messages found in topic to resolve")
				}
				msgID = openMsgs[len(openMsgs)-1].ID
			}
			_, err = doZulip(ctx, http.MethodPatch, fmt.Sprintf("/api/v1/messages/%d", msgID), url.Values{
				"topic":          {"✔ " + in.Topic},
				"propagate_mode": {"change_all"},
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"already_resolved": false, "topic": "✔ " + in.Topic}, nil
		},
	}); err != nil {
		os.Exit(1)
	}
}
