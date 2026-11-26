package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSnapshotAndReplayShareCommitBoundary(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	first := addReady(t, s)
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].GraphRevision != 1 || len(snapshot.Nodes) != 1 {
		t.Fatalf("initial snapshot %+v", snapshot)
	}

	second, err := s.AddNode(ctx, NewNode{RunID: first.RunID, Kind: "tool", Name: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(ctx, Edge{RunID: first.RunID, FromID: first.ID, ToID: second.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAttempt(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	events, err := s.Replay(ctx, snapshot.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node_added", "edge_added", "attempt_started"}
	if len(events) != len(want) {
		t.Fatalf("replayed %d events, want %d", len(events), len(want))
	}
	for idx, event := range events {
		if event.Kind != want[idx] || event.Sequence != snapshot.Cursor+int64(idx)+1 {
			t.Fatalf("event %d = %+v", idx, event)
		}
	}
	latest, err := s.Snapshot(ctx)
	if err != nil || latest.Cursor != events[len(events)-1].Sequence || latest.Runs[0].GraphRevision != 3 {
		t.Fatalf("latest snapshot %+v, %v", latest, err)
	}
}

func TestConcurrentSnapshotsStayConsistent(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runID, err := s.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := range 80 {
			_, err := s.AddNode(ctx, NewNode{RunID: runID, Kind: "tool", Name: fmt.Sprint(i)})
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for {
		snapshot, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := int64(len(snapshot.Nodes))
		if snapshot.Runs[0].GraphRevision != n || snapshot.Cursor != n+1 {
			t.Fatalf("inconsistent snapshot: revision=%d nodes=%d cursor=%d", snapshot.Runs[0].GraphRevision, n, snapshot.Cursor)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}

func TestReplayWindowBounds(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	runID, err := s.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = s.write(ctx, func(tx *writeTx) error {
		for range ReplayWindow {
			if err := tx.event(ctx, runID, nil, nil, "fixture", struct{}{}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []int64{0, ReplayWindow + 2} {
		if _, err := s.Replay(ctx, cursor); !errors.Is(err, ErrCursorExpired) {
			t.Fatalf("replay cursor %d: %v", cursor, err)
		}
	}
	batch, err := s.Replay(ctx, 1)
	if err != nil || len(batch) != ReplayBatch || batch[0].Sequence != 2 {
		t.Fatalf("boundary replay: %d events, %v", len(batch), err)
	}
}
