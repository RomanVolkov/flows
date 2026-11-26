// Package store persists workflow state and its event log in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const schemaVersion = 2

type Store struct {
	db *sql.DB

	writer  sync.Mutex
	events  sync.Mutex
	changed chan struct{}
}

// Open initializes the database. The daemon holds its service lock throughout
// Open and the database lifetime so migrations have a single owner.
func Open(ctx context.Context, file string) (*Store, error) {
	if !filepath.IsAbs(file) {
		return nil, fmt.Errorf("database path must be absolute: %q", file)
	}
	uri := url.URL{Scheme: "file", Path: file}
	params := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}
	uri.RawQuery = params.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, changed: make(chan struct{})}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init(ctx context.Context) error {
	var journal string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		return err
	}
	if journal != "wal" {
		return fmt.Errorf("expected WAL journal, found %q", journal)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, schemaVersion)
	}
	if version < 1 {
		script, err := migrations.ReadFile("migrations/001_initial.sql")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			return err
		}
	}
	if version < 2 {
		script, err := migrations.ReadFile("migrations/002_usage.sql")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version=2"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DB exposes the underlying SQLite handle for domain packages like usage.
func (s *Store) DB() *sql.DB { return s.db }

// WriteTx executes fn under the store's serialized writer lock.
func (s *Store) WriteTx(ctx context.Context, fn func(*sql.Tx, int64) error) error {
	return s.write(ctx, func(tx *writeTx) error {
		return fn(tx.Tx, tx.now)
	})
}

// Changes closes after an event transaction commits. A reader can take this
// channel before reading its cursor, then wait without missing a commit.
func (s *Store) Changes() <-chan struct{} {
	s.events.Lock()
	defer s.events.Unlock()
	return s.changed
}

type writeTx struct {
	*sql.Tx
	now     int64
	emitted bool
}

func (s *Store) write(ctx context.Context, run func(*writeTx) error) error {
	s.writer.Lock()
	defer s.writer.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	change := &writeTx{Tx: tx, now: time.Now().UnixNano()}
	if err := run(change); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if change.emitted {
		s.events.Lock()
		close(s.changed)
		s.changed = make(chan struct{})
		s.events.Unlock()
	}
	return nil
}

func (t *writeTx) event(ctx context.Context, runID string, nodeID, attemptID *string, kind string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = t.ExecContext(ctx, `
		INSERT INTO events(run_id, node_id, attempt_id, kind, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, runID, nodeID, attemptID, kind, string(payload), t.now)
	if err == nil {
		t.emitted = true
	}
	return err
}
