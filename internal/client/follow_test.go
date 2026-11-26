package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/store"
)

func TestFollowReconnectsAndResnapshots(t *testing.T) {
	var snapshots atomic.Int32
	var retries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/workspace":
			n := snapshots.Add(1)
			view := store.Workspace{Cursor: 10, Nodes: []store.Node{{ID: "kept"}, {ID: "removed"}}}
			if n > 1 {
				view.Cursor = 20
				view.Nodes = []store.Node{{ID: "kept"}, {ID: "new"}}
			}
			_ = json.NewEncoder(w).Encode(view)
		case "/v1/events":
			write := func(seq int) {
				fmt.Fprintf(w, "id: %d\ndata: {\"sequence\":%d,\"kind\":\"node_status_changed\"}\n\n", seq, seq)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			switch r.URL.Query().Get("after") {
			case "10":
				write(11)
				// The server exits before the second event can be delivered.
			case "11":
				retries.Add(1)
				write(11)
				write(12)
			case "12":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGone)
				_ = json.NewEncoder(w).Encode(localapi.Error{Code: "cursor_expired", Message: "cursor expired"})
			case "20":
				write(21)
			default:
				t.Errorf("unexpected cursor %s", r.URL.RawQuery)
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}}
	c := &Client{http: &http.Client{Transport: transport}}
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	var applied []int64
	var selections [][]string
	err := c.Follow(ctx, WorkspaceObserver{
		Selection: func() []string { return []string{"kept", "removed"} },
		Reset: func(_ store.Workspace, selection []string) {
			selections = append(selections, selection)
		},
		Apply: func(event store.Event) {
			applied = append(applied, event.Sequence)
			if event.Sequence == 21 {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("follow: %v", err)
	}
	if !reflect.DeepEqual(applied, []int64{11, 12, 21}) || retries.Load() != 1 {
		t.Fatalf("applied %v, reconnects %d", applied, retries.Load())
	}
	if !reflect.DeepEqual(selections, [][]string{{"kept", "removed"}, {"kept"}}) {
		t.Fatalf("restored selections %v", selections)
	}
}
