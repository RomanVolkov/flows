package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const ReplayWindow = 1024
const ReplayBatch = 128

var ErrCursorExpired = errors.New("workspace event cursor expired")

type Run struct {
	ID                   string  `json:"id"`
	DefinitionRevisionID *string `json:"definition_revision_id,omitempty"`
	GraphRevision        int64   `json:"graph_revision"`
	CreatedAt            int64   `json:"created_at"`
}

type Workspace struct {
	Cursor   int64     `json:"cursor"`
	Runs     []Run     `json:"runs"`
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges"`
	Attempts []Attempt `json:"attempts"`
}

type Event struct {
	Sequence  int64           `json:"sequence"`
	RunID     string          `json:"run_id"`
	NodeID    *string         `json:"node_id,omitempty"`
	AttemptID *string         `json:"attempt_id,omitempty"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt int64           `json:"created_at"`
}

func (s *Store) Snapshot(ctx context.Context) (Workspace, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback()
	var view Workspace
	if view.Cursor, err = eventCursor(ctx, tx); err != nil {
		return Workspace{}, err
	}
	view.Runs, err = collect(ctx, tx, `
		SELECT id, definition_revision_id, graph_revision, created_at FROM run_roots ORDER BY created_at, id
	`, func(row scanner) (Run, error) {
		var run Run
		err := row.Scan(&run.ID, &run.DefinitionRevisionID, &run.GraphRevision, &run.CreatedAt)
		return run, err
	})
	if err != nil {
		return Workspace{}, err
	}
	view.Nodes, err = collect(ctx, tx, "SELECT "+nodeColumns+" FROM nodes ORDER BY created_at, id", scanNode)
	if err != nil {
		return Workspace{}, err
	}
	unknown, err := collect(ctx, tx, "SELECT node_id FROM workflow_steps WHERE in_flight_unknown=1", func(row scanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return Workspace{}, err
	}
	unknownByID := make(map[string]bool, len(unknown))
	for _, id := range unknown {
		unknownByID[id] = true
	}
	for i := range view.Nodes {
		if view.Nodes[i].Status == Interrupted && unknownByID[view.Nodes[i].ID] {
			view.Nodes[i].Detail = workflowUnknownOutcome
		}
	}
	view.Edges, err = collect(ctx, tx, `
		SELECT id, run_id, from_id, to_id, milestone FROM edges ORDER BY run_id, id
	`, func(row scanner) (Edge, error) {
		var edge Edge
		err := row.Scan(&edge.ID, &edge.RunID, &edge.FromID, &edge.ToID, &edge.Milestone)
		return edge, err
	})
	if err != nil {
		return Workspace{}, err
	}
	view.Attempts, err = collect(ctx, tx, `
		SELECT id, node_id, number, status, started_at, finished_at FROM node_attempts ORDER BY started_at, id
	`, func(row scanner) (Attempt, error) {
		var attempt Attempt
		err := row.Scan(&attempt.ID, &attempt.NodeID, &attempt.Number, &attempt.Status, &attempt.StartedAt, &attempt.FinishedAt)
		return attempt, err
	})
	if err != nil {
		return Workspace{}, err
	}
	return view, tx.Commit()
}

func (s *Store) Replay(ctx context.Context, after int64) ([]Event, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	cursor, err := eventCursor(ctx, tx)
	if err != nil {
		return nil, err
	}
	var oldest int64
	err = tx.QueryRowContext(ctx, `SELECT sequence FROM events ORDER BY sequence DESC LIMIT 1 OFFSET ?`, ReplayWindow-1).Scan(&oldest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if after < 0 || after > cursor || (oldest > 0 && after < oldest-1) {
		return nil, fmt.Errorf("%w: after=%d latest=%d", ErrCursorExpired, after, cursor)
	}

	events, err := collect(ctx, tx, `
		SELECT sequence, run_id, node_id, attempt_id, kind, payload_json, created_at
		FROM events WHERE sequence>? ORDER BY sequence LIMIT ?
	`, func(row scanner) (Event, error) {
		var event Event
		var payload []byte
		if err := row.Scan(&event.Sequence, &event.RunID, &event.NodeID, &event.AttemptID, &event.Kind, &payload, &event.CreatedAt); err != nil {
			return Event{}, err
		}
		event.Payload = payload
		return event, nil
	}, after, ReplayBatch)
	if err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

func eventCursor(ctx context.Context, tx *sql.Tx) (int64, error) {
	var cursor int64
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) FROM events").Scan(&cursor)
	return cursor, err
}

func collect[T any](ctx context.Context, tx *sql.Tx, query string, scan func(scanner) (T, error), args ...any) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []T{}
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
