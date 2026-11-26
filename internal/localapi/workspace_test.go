package localapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/store"
)

func TestWorkspaceAndEventsOverPrivateSocket(t *testing.T) {
	home := homeForTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(home.State, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	runID, err := db.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := db.AddNode(ctx, store.NewNode{RunID: runID, Kind: "tool", Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	done := runDaemon(ctx, home)
	c := connectRunning(t, ctx, home)
	snapshot, err := c.Workspace(ctx)
	if err != nil || snapshot.Cursor != 2 || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != node.ID {
		t.Fatalf("snapshot %+v, %v", snapshot, err)
	}

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", home.Socket())
	}}
	defer transport.CloseIdleConnections()
	raw := &http.Client{Transport: transport}
	get := func(after int64) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://flows.local/v1/events?after=%d", after), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	stale := get(3)
	var apiErr localapi.Error
	if err := json.NewDecoder(stale.Body).Decode(&apiErr); err != nil || stale.StatusCode != http.StatusGone || apiErr.Code != "cursor_expired" {
		t.Fatalf("expired cursor = %d %+v, %v", stale.StatusCode, apiErr, err)
	}
	stale.Body.Close()

	stream := get(1)
	if value := stream.Header.Get("Content-Type"); value != "text/event-stream" {
		t.Fatalf("unexpected stream type: %s", value)
	}
	reader := bufio.NewReader(stream.Body)
	for _, want := range []string{": connected\n", "\n", "id: 2\n"} {
		line, err := reader.ReadString('\n')
		if err != nil || line != want {
			t.Fatalf("stream line %q, %v, want %q", line, err, want)
		}
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitForExit(t, ctx, done)
}
