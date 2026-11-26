package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "flows test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func scalar[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var value T
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMigrationAndConnectionPragmas(t *testing.T) {
	file := filepath.Join(t.TempDir(), "flows.db")
	ctx := t.Context()
	for range 2 {
		s, err := Open(ctx, file)
		if err != nil {
			t.Fatal(err)
		}
		if value := scalar[int](t, s.db, "PRAGMA user_version"); value != schemaVersion {
			t.Errorf("schema version = %d", value)
		}
		if value := scalar[string](t, s.db, "PRAGMA journal_mode"); value != "wal" {
			t.Errorf("journal mode = %s", value)
		}
		if value := scalar[int](t, s.db, "PRAGMA foreign_keys"); value != 1 {
			t.Errorf("foreign keys = %d", value)
		}
		if value := scalar[int](t, s.db, "PRAGMA busy_timeout"); value < 1000 {
			t.Errorf("busy timeout = %d ms", value)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationFailureRollsBackDDL(t *testing.T) {
	file := filepath.Join(t.TempDir(), "partial.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE milestones(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(t.Context(), file); err == nil {
		s.Close()
		t.Fatal("expected schema name collision to fail")
	}
	if count := scalar[int](t, db, "SELECT count(*) FROM sqlite_master WHERE name='run_roots'"); count != 0 {
		t.Fatal("failed migration left partial tables")
	}
	if version := scalar[int](t, db, "PRAGMA user_version"); version != 0 {
		t.Fatalf("failed migration recorded schema version %d", version)
	}
}

func addReady(t *testing.T, s *Store) Node {
	t.Helper()
	ctx := t.Context()
	run, err := s.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.AddNode(ctx, NewNode{RunID: run, Kind: "session", Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func waitState(t *testing.T, s *Store, ctx context.Context, id string, status Status) Node {
	t.Helper()
	node, err := s.Node(ctx, id)
	if err != nil || node.Status != status {
		t.Fatalf("node %s = %+v, err = %v, want %s", id, node, err, status)
	}
	return node
}

func TestWaitReasonsRetriesAndRelationships(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	parent := addReady(t, s)
	child, err := s.AddNode(ctx, NewNode{
		RunID: parent.RunID, ParentID: &parent.ID, Kind: "tool", Name: "dependent",
		Wait: &WaitReason{Kind: "edge", Ref: parent.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(ctx, Edge{RunID: parent.RunID, FromID: parent.ID, ToID: child.ID}); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, s, ctx, child.ID, WaitingForDependencies); got.Wait == nil || got.Wait.Ref != parent.ID {
		t.Fatalf("unexpected edge wait %+v", got.Wait)
	}
	timer := time.Now().Add(time.Minute).UnixNano()
	if err := s.Transition(ctx, child.ID, WaitingForDependencies, &WaitReason{Kind: "timer", Until: &timer}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAttempt(ctx, child.ID); !errors.Is(err, ErrTransition) {
		t.Fatalf("started an attempt while waiting: %v", err)
	}
	if err := s.Transition(ctx, child.ID, Ready, nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.StartAttempt(ctx, child.ID)
	if err != nil || first.Number != 1 {
		t.Fatalf("first attempt %+v, %v", first, err)
	}
	if err := s.Transition(ctx, child.ID, WaitingForInput, &WaitReason{Kind: "human", Ref: "question-1"}); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, s, ctx, child.ID, WaitingForInput); got.Wait == nil || got.Wait.Ref != "question-1" {
		t.Fatalf("unexpected human wait %+v", got.Wait)
	}
	for _, state := range []Status{Stopping, Interrupted, Ready} {
		if err := s.Transition(ctx, child.ID, state, nil); err != nil {
			t.Fatal(err)
		}
	}
	second, err := s.StartAttempt(ctx, child.ID)
	if err != nil || second.Number != 2 || second.ID == first.ID {
		t.Fatalf("second attempt %+v, %v", second, err)
	}
	other := addReady(t, s)
	if _, err := s.AddEdge(ctx, Edge{RunID: child.RunID, FromID: other.ID, ToID: child.ID}); err == nil {
		t.Fatal("added an edge across runs")
	}
}

func TestCompletionRollsBackStateAndEventAndReplays(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	node := addReady(t, s)
	attempt, err := s.StartAttempt(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := scalar[int](t, s.db, "SELECT count(*) FROM events")
	changed := s.Changes()
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER fail_completion BEFORE INSERT ON events
		WHEN NEW.kind='completion_recorded'
		BEGIN SELECT RAISE(ABORT, 'forced event failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, attempt.ID, "done", Succeeded, []byte("result")); err == nil {
		t.Fatal("completed despite event failure")
	}
	waitState(t, s, ctx, node.ID, Running)
	if got := scalar[string](t, s.db, "SELECT status FROM node_attempts WHERE id=?", attempt.ID); got != string(Running) {
		t.Fatalf("attempt status changed to %s", got)
	}
	if count := scalar[int](t, s.db, "SELECT count(*) FROM completion_dispositions"); count != 0 {
		t.Fatal("rolled-back disposition is visible")
	}
	if count := scalar[int](t, s.db, "SELECT count(*) FROM events"); count != events {
		t.Fatal("rolled-back event is visible")
	}
	select {
	case <-changed:
		t.Fatal("rolled-back transaction notified subscribers")
	default:
	}

	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_completion"); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Complete(ctx, attempt.ID, "done", Succeeded, []byte("result"))
	if err != nil || accepted.Outcome != "accepted" {
		t.Fatalf("completion %+v, %v", accepted, err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("committed completion did not notify subscribers")
	}
	waitState(t, s, ctx, node.ID, Succeeded)
	retry, err := s.Complete(ctx, attempt.ID, "done", Succeeded, []byte("result"))
	if err != nil || retry != accepted {
		t.Fatalf("retry %+v, %v", retry, err)
	}
	if _, err := s.Complete(ctx, attempt.ID, "done", Succeeded, []byte("different")); !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("conflicting completion: %v", err)
	}
	rejected, err := s.Complete(ctx, attempt.ID, "late", Failed, []byte("late result"))
	if err != nil || rejected.Outcome != "rejected" {
		t.Fatalf("late result %+v, %v", rejected, err)
	}
	if count := scalar[int](t, s.db, "SELECT count(*) FROM events"); count != events+2 {
		t.Fatalf("completion events = %d, want %d", count, events+2)
	}
}
