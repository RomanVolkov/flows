package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrNodeActive = errors.New("node subtree still has an active attempt")

// RemoveSubtree permanently removes a node, its descendants, and their stored
// execution history. Log files remain on disk. A single node_removed event
// tells connected clients to refresh their snapshot.
func (s *Store) RemoveSubtree(ctx context.Context, nodeID string) ([]string, error) {
	var removed []string
	err := s.write(ctx, func(tx *writeTx) error {
		type item struct {
			id, runID string
			depth     int
		}
		var subtree []item
		rows, err := tx.QueryContext(ctx, `
			WITH RECURSIVE subtree(id, run_id, depth) AS (
				SELECT id, run_id, 0 FROM nodes WHERE id = ?
				UNION ALL
				SELECT n.id, n.run_id, subtree.depth + 1
				FROM nodes n JOIN subtree ON n.run_id = subtree.run_id AND n.parent_id = subtree.id
			)
			SELECT id, run_id, depth FROM subtree ORDER BY depth DESC, id
		`, nodeID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var next item
			if err := rows.Scan(&next.id, &next.runID, &next.depth); err != nil {
				rows.Close()
				return err
			}
			subtree = append(subtree, next)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(subtree) == 0 {
			return sql.ErrNoRows
		}
		runID := subtree[0].runID
		ids := make([]string, 0, len(subtree))
		for _, item := range subtree {
			ids = append(ids, item.id)
			var status Status
			if err := tx.QueryRowContext(ctx, "SELECT status FROM nodes WHERE id=?", item.id).Scan(&status); err != nil {
				return err
			}
			if status == Running || status == WaitingForInput || status == Stopping {
				return fmt.Errorf("%w: %s", ErrNodeActive, item.id)
			}
		}
		idMarks, idArgs := sqlList(ids)
		var attempts []string
		rows, err = tx.QueryContext(ctx, "SELECT id FROM node_attempts WHERE node_id IN ("+idMarks+")", idArgs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			attempts = append(attempts, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(attempts) > 0 {
			attemptMarks, attemptArgs := sqlList(attempts)
			for _, query := range []string{
				"DELETE FROM events WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM completion_dispositions WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM milestones WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM sessions WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM activities WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM artifacts WHERE attempt_id IN (" + attemptMarks + ")",
				"DELETE FROM usage_receipts WHERE attempt_id IN (" + attemptMarks + ")",
			} {
				if _, err := tx.ExecContext(ctx, query, attemptArgs...); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE node_id IN ("+idMarks+")", idArgs...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id IN ("+idMarks+") OR to_id IN ("+idMarks+")", append(idArgs, idArgs...)...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM usage_receipts WHERE node_id IN ("+idMarks+")", idArgs...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE nodes SET active_attempt_id=NULL WHERE id IN ("+idMarks+")", idArgs...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_attempts WHERE node_id IN ("+idMarks+")", idArgs...); err != nil {
			return err
		}
		for _, item := range subtree { // child-first, respecting the parent foreign key
			if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id=?", item.id); err != nil {
				return err
			}
		}
		if err := tx.advanceGraph(ctx, runID); err != nil {
			return err
		}
		if err := tx.event(ctx, runID, nil, nil, "nodes_removed", map[string][]string{"node_ids": ids}); err != nil {
			return err
		}
		removed = ids
		return nil
	})
	return removed, err
}

func sqlList(values []string) (string, []any) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return marks, args
}
