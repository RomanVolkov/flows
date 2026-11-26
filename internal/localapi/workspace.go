package localapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

func workspace(w http.ResponseWriter, r *http.Request, state *store.Store) {
	view, err := state.Snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot_failed", "could not read workspace")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func events(w http.ResponseWriter, r *http.Request, state *store.Store) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		raw = "0"
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "event cursor must be a nonnegative integer")
		return
	}

	controller := http.NewResponseController(w)
	write := func(message string) error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := fmt.Fprint(w, message); err != nil {
			return err
		}
		return controller.Flush()
	}
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	started := false
	for {
		changes := state.Changes()
		batch, err := state.Replay(r.Context(), cursor)
		if err != nil {
			if !started {
				if errors.Is(err, store.ErrCursorExpired) {
					writeError(w, http.StatusGone, "cursor_expired", "event cursor expired")
				} else {
					writeError(w, http.StatusInternalServerError, "replay_failed", "could not read event replay")
				}
			}
			return
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			if err := write(": connected\n\n"); err != nil {
				return
			}
			started = true
		}
		for _, event := range batch {
			payload, err := json.Marshal(event)
			if err != nil {
				return
			}
			if err := write(fmt.Sprintf("id: %d\ndata: %s\n\n", event.Sequence, payload)); err != nil {
				return
			}
			cursor = event.Sequence
		}
		if len(batch) != 0 {
			continue
		}

		select {
		case <-r.Context().Done():
			return
		case <-changes:
		case <-heartbeat.C:
			if err := write(": heartbeat\n\n"); err != nil {
				return
			}
		}
	}
}
