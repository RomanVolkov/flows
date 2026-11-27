package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

type Status string

const (
	WaitingForDependencies Status = "waiting_for_dependencies"
	Ready                  Status = "ready"
	Running                Status = "running"
	WaitingForInput        Status = "waiting_for_input"
	Stopping               Status = "stopping"
	Succeeded              Status = "succeeded"
	Failed                 Status = "failed"
	Interrupted            Status = "interrupted"
	Skipped                Status = "skipped"
	Cancelled              Status = "cancelled"
)

type WaitReason struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref,omitempty"`
	Until *int64 `json:"until,omitempty"`
}

type Node struct {
	ID        string      `json:"id"`
	RunID     string      `json:"run_id"`
	ParentID  *string     `json:"parent_id,omitempty"`
	Kind      string      `json:"kind"`
	Name      string      `json:"name"`
	Status    Status      `json:"status"`
	Wait      *WaitReason `json:"wait,omitempty"`
	AttemptID *string     `json:"attempt_id,omitempty"`
	CreatedAt int64       `json:"created_at"`
	UpdatedAt int64       `json:"updated_at"`
}

type NewNode struct {
	RunID    string
	ParentID *string
	Kind     string
	Name     string
	Wait     *WaitReason
}

type Edge struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	FromID    string `json:"from_id"`
	ToID      string `json:"to_id"`
	Milestone string `json:"milestone,omitempty"`
}

type Attempt struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Number     int    `json:"number"`
	Status     Status `json:"status"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt *int64 `json:"finished_at,omitempty"`
}

var ErrTransition = errors.New("invalid node transition")

func (s *Store) CreateRun(ctx context.Context, definitionRevision *string) (string, error) {
	id := rand.Text()
	err := s.write(ctx, func(tx *writeTx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_roots(id, definition_revision_id, created_at) VALUES (?, ?, ?)
		`, id, definitionRevision, tx.now); err != nil {
			return err
		}
		return tx.event(ctx, id, nil, nil, "run_created", struct{}{})
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) AddNode(ctx context.Context, options NewNode) (Node, error) {
	status := Ready
	if options.Wait != nil {
		status = WaitingForDependencies
	}
	if err := validateWait(status, options.Wait); err != nil {
		return Node{}, err
	}
	node := Node{
		ID: rand.Text(), RunID: options.RunID, ParentID: options.ParentID,
		Kind: options.Kind, Name: options.Name, Status: status, Wait: options.Wait,
	}
	err := s.write(ctx, func(tx *writeTx) error {
		node.CreatedAt, node.UpdatedAt = tx.now, tx.now
		var kind, ref *string
		var until *int64
		if node.Wait != nil {
			kind, ref, until = &node.Wait.Kind, &node.Wait.Ref, node.Wait.Until
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO nodes(id, run_id, parent_id, kind, name, status, wait_kind, wait_ref, wait_until, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, node.ID, node.RunID, node.ParentID, node.Kind, node.Name, node.Status, kind, ref, until, tx.now, tx.now); err != nil {
			return err
		}
		if err := tx.advanceGraph(ctx, node.RunID); err != nil {
			return err
		}
		return tx.event(ctx, node.RunID, &node.ID, nil, "node_added", node)
	})
	if err != nil {
		return Node{}, err
	}
	return node, nil
}

func (s *Store) AddEdge(ctx context.Context, edge Edge) (Edge, error) {
	edge.ID = rand.Text()
	err := s.write(ctx, func(tx *writeTx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO edges(id, run_id, from_id, to_id, milestone) VALUES (?, ?, ?, ?, ?)
		`, edge.ID, edge.RunID, edge.FromID, edge.ToID, edge.Milestone); err != nil {
			return err
		}
		if err := tx.advanceGraph(ctx, edge.RunID); err != nil {
			return err
		}
		return tx.event(ctx, edge.RunID, &edge.ToID, nil, "edge_added", edge)
	})
	if err != nil {
		return Edge{}, err
	}
	return edge, nil
}

func (t *writeTx) advanceGraph(ctx context.Context, runID string) error {
	_, err := t.ExecContext(ctx, "UPDATE run_roots SET graph_revision=graph_revision+1 WHERE id=?", runID)
	return err
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type scanner interface {
	Scan(...any) error
}

const nodeColumns = "id, run_id, parent_id, kind, name, status, wait_kind, wait_ref, wait_until, active_attempt_id, created_at, updated_at"

func readNode(ctx context.Context, db queryer, id string) (Node, error) {
	row := db.QueryRowContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE id=?", id)
	return scanNode(row)
}

func scanNode(row scanner) (Node, error) {
	var node Node
	var kind, ref *string
	var until *int64
	err := row.Scan(&node.ID, &node.RunID, &node.ParentID, &node.Kind, &node.Name, &node.Status,
		&kind, &ref, &until, &node.AttemptID, &node.CreatedAt, &node.UpdatedAt)
	if err != nil {
		return Node{}, err
	}
	if kind != nil {
		node.Wait = &WaitReason{Kind: *kind, Until: until}
		if ref != nil {
			node.Wait.Ref = *ref
		}
	}
	return node, nil
}

func (s *Store) Node(ctx context.Context, id string) (Node, error) {
	return readNode(ctx, s.db, id)
}

func (s *Store) StartAttempt(ctx context.Context, nodeID string) (Attempt, error) {
	attempt := Attempt{ID: rand.Text(), NodeID: nodeID, Status: Running}
	err := s.write(ctx, func(tx *writeTx) error {
		node, err := readNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if node.Status != Ready {
			return fmt.Errorf("%w: start attempt from %s", ErrTransition, node.Status)
		}
		attempt.StartedAt = tx.now
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO node_attempts(id, node_id, number, status, started_at)
			VALUES (?, ?, (SELECT COALESCE(MAX(number), 0)+1 FROM node_attempts WHERE node_id=?), ?, ?)
			RETURNING number
		`, attempt.ID, nodeID, nodeID, attempt.Status, attempt.StartedAt).Scan(&attempt.Number); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes SET status=?, active_attempt_id=?, updated_at=? WHERE id=?
		`, Running, attempt.ID, tx.now, nodeID); err != nil {
			return err
		}
		return tx.event(ctx, node.RunID, &node.ID, &attempt.ID, "attempt_started", attempt)
	})
	if err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

// ResumeAttempt starts a new attempt for a stopped or interrupted node while
// keeping its identity, parent, edges, and prior attempt history intact.
func (s *Store) ResumeAttempt(ctx context.Context, nodeID string) (Attempt, error) {
	attempt := Attempt{ID: rand.Text(), NodeID: nodeID, Status: Running}
	err := s.write(ctx, func(tx *writeTx) error {
		node, err := readNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if node.Status != Cancelled && node.Status != Interrupted && node.Status != Failed {
			return fmt.Errorf("%w: resume attempt from %s", ErrTransition, node.Status)
		}
		attempt.StartedAt = tx.now
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO node_attempts(id, node_id, number, status, started_at)
			VALUES (?, ?, (SELECT COALESCE(MAX(number), 0)+1 FROM node_attempts WHERE node_id=?), ?, ?)
			RETURNING number
		`, attempt.ID, nodeID, nodeID, attempt.Status, attempt.StartedAt).Scan(&attempt.Number); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes SET status=?, active_attempt_id=?, wait_kind=NULL, wait_ref=NULL, wait_until=NULL, updated_at=? WHERE id=?
		`, Running, attempt.ID, tx.now, nodeID); err != nil {
			return err
		}
		return tx.event(ctx, node.RunID, &node.ID, &attempt.ID, "attempt_resumed", attempt)
	})
	if err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (s *Store) Transition(ctx context.Context, nodeID string, next Status, wait *WaitReason) error {
	if err := validateWait(next, wait); err != nil {
		return err
	}
	return s.write(ctx, func(tx *writeTx) error {
		node, err := readNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if !allows(node.Status, next) {
			return fmt.Errorf("%w: %s to %s", ErrTransition, node.Status, next)
		}
		if err := tx.updateNode(ctx, &node, next, wait); err != nil {
			return err
		}
		return tx.event(ctx, node.RunID, &node.ID, node.AttemptID, "node_status_changed", node)
	})
}

func validateWait(status Status, wait *WaitReason) error {
	switch status {
	case WaitingForDependencies:
		if wait != nil && ((wait.Kind == "edge" && wait.Ref != "" && wait.Until == nil) ||
			(wait.Kind == "timer" && wait.Until != nil)) {
			return nil
		}
	case WaitingForInput:
		if wait != nil && wait.Kind == "human" && wait.Ref != "" && wait.Until == nil {
			return nil
		}
	default:
		if wait == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid wait reason for %s", ErrTransition, status)
}

func allows(current, next Status) bool {
	switch next {
	case WaitingForDependencies:
		return current == Ready || current == WaitingForDependencies
	case Ready:
		return current == WaitingForDependencies || current == Interrupted || current == Failed
	case WaitingForInput:
		return current == Running || current == WaitingForInput
	case Running:
		return current == WaitingForInput
	case Stopping:
		return current == Running || current == WaitingForInput
	case Interrupted:
		return current == Running || current == WaitingForInput || current == Stopping
	case Skipped:
		return current == WaitingForDependencies || current == Ready
	case Cancelled:
		return current == WaitingForDependencies || current == Ready || current == Stopping
	default:
		return false
	}
}

func (t *writeTx) updateNode(ctx context.Context, node *Node, next Status, wait *WaitReason) error {
	var waitKind, waitRef *string
	var waitUntil *int64
	if wait != nil {
		waitKind, waitRef, waitUntil = &wait.Kind, &wait.Ref, wait.Until
	}
	attempt := node.AttemptID
	var finished *int64
	switch next {
	case Succeeded, Failed, Interrupted, Cancelled:
		finished = &t.now
	case Ready, WaitingForDependencies:
		attempt = nil
	}
	if _, err := t.ExecContext(ctx, `
		UPDATE nodes SET status=?, wait_kind=?, wait_ref=?, wait_until=?, active_attempt_id=?, updated_at=? WHERE id=?
	`, next, waitKind, waitRef, waitUntil, attempt, t.now, node.ID); err != nil {
		return err
	}
	if attempt != nil {
		if _, err := t.ExecContext(ctx, `
			UPDATE node_attempts SET status=?, finished_at=? WHERE id=? AND finished_at IS NULL
		`, next, finished, attempt); err != nil {
			return err
		}
	}
	node.Status, node.Wait, node.AttemptID, node.UpdatedAt = next, wait, attempt, t.now
	return nil
}
