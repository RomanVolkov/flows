package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/store"
)

type WorkspaceObserver struct {
	Selection func() []string
	Reset     func(store.Workspace, []string)
	Apply     func(store.Event)
}

func (c *Client) Workspace(ctx context.Context) (store.Workspace, error) {
	var workspace store.Workspace
	err := c.request(ctx, http.MethodGet, "/v1/workspace", &workspace)
	return workspace, err
}

var errInvalidStream = errors.New("invalid workspace event stream")

// Follow starts with a snapshot and applies each committed event once in
// sequence order. A stale cursor requests a new snapshot and selection.
func (c *Client) Follow(ctx context.Context, observer WorkspaceObserver) error {
	if observer.Reset == nil || observer.Apply == nil {
		return fmt.Errorf("workspace observer requires snapshot and event handlers")
	}
	const minBackoff = 50 * time.Millisecond
	backoff := minBackoff
	var cursor int64
	hydrated := false
	for ctx.Err() == nil {
		var err error
		if !hydrated {
			var view store.Workspace
			view, err = c.Workspace(ctx)
			if err == nil {
				observer.Reset(view, retainedSelection(view, observer.Selection))
				cursor, hydrated = view.Cursor, true
				backoff = minBackoff
			}
		}
		if hydrated {
			before := cursor
			err = c.followStream(ctx, &cursor, observer.Apply)
			if cursor != before {
				backoff = minBackoff
			}
		}
		var apiErr *localapi.Error
		switch {
		case errors.As(err, &apiErr):
			if apiErr.Code != "cursor_expired" {
				return err
			}
			hydrated = false
		case errors.Is(err, errInvalidStream):
			return err
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(2*backoff, 2*time.Second)
	}
	return ctx.Err()
}

func retainedSelection(view store.Workspace, selection func() []string) []string {
	if selection == nil {
		return nil
	}
	present := make(map[string]bool, len(view.Nodes))
	for _, node := range view.Nodes {
		present[node.ID] = true
	}
	var retained []string
	for _, id := range selection() {
		if present[id] {
			retained = append(retained, id)
		}
	}
	return retained
}

func (c *Client) followStream(ctx context.Context, cursor *int64, apply func(store.Event)) error {
	path := "/v1/events?after=" + strconv.FormatInt(*cursor, 10)
	resp, err := c.send(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" {
		return fmt.Errorf("%w: unexpected content type", errInvalidStream)
	}
	return scanEvents(resp.Body, cursor, apply)
}

func scanEvents(body io.Reader, cursor *int64, apply func(store.Event)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 16*1024), localapi.MaxBodyBytes)
	var payload strings.Builder
	var id int64
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if payload.Len() != 0 {
				var event store.Event
				if err := json.Unmarshal([]byte(payload.String()), &event); err != nil || id <= 0 || event.Sequence != id {
					return fmt.Errorf("%w: event payload or ID", errInvalidStream)
				}
				if event.Sequence > *cursor {
					apply(event)
					*cursor = event.Sequence
				}
			}
			payload.Reset()
			id = 0
		case strings.HasPrefix(line, "id:"):
			value, err := strconv.ParseInt(strings.TrimSpace(line[3:]), 10, 64)
			if err != nil {
				return fmt.Errorf("%w: event ID", errInvalidStream)
			}
			id = value
		case strings.HasPrefix(line, "data:"):
			if payload.Len()+len(line) > localapi.MaxBodyBytes {
				return fmt.Errorf("%w: event exceeds 1 MiB", errInvalidStream)
			}
			payload.WriteString(strings.TrimPrefix(line[5:], " "))
			payload.WriteByte('\n')
		}
	}
	return scanner.Err()
}
