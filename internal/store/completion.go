package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

type Disposition struct {
	AttemptID string `json:"attempt_id"`
	EventID   string `json:"event_id"`
	Outcome   string `json:"disposition"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

var ErrCompletionConflict = errors.New("completion event ID already used with a different payload")

// Complete records one immutable outcome per producer event. An ineligible
// attempt gets a durable rejection so a retry can receive the same answer.
func (s *Store) Complete(ctx context.Context, attemptID, eventID string, status Status, payload []byte) (Disposition, error) {
	if attemptID == "" || eventID == "" || (status != Succeeded && status != Failed) {
		return Disposition{}, fmt.Errorf("invalid completion event")
	}
	hasher := sha256.New()
	hasher.Write([]byte(string(status) + "\x00"))
	hasher.Write(payload)
	digest := hex.EncodeToString(hasher.Sum(nil))
	outcome := Disposition{AttemptID: attemptID, EventID: eventID}
	err := s.write(ctx, func(tx *writeTx) error {
		var previousDigest string
		err := tx.QueryRowContext(ctx, `
			SELECT payload_digest, disposition, COALESCE(reason, ''), created_at
			FROM completion_dispositions WHERE attempt_id=? AND event_id=?
		`, attemptID, eventID).Scan(&previousDigest, &outcome.Outcome, &outcome.Reason, &outcome.CreatedAt)
		if err == nil {
			if previousDigest != digest {
				return ErrCompletionConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var nodeID string
		if err := tx.QueryRowContext(ctx, "SELECT node_id FROM node_attempts WHERE id=?", attemptID).Scan(&nodeID); err != nil {
			return err
		}
		node, err := readNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		outcome.CreatedAt = tx.now
		outcome.Outcome = "rejected"
		outcome.Reason = "attempt_no_longer_active"
		if node.AttemptID != nil && *node.AttemptID == attemptID && (node.Status == Running || node.Status == WaitingForInput) {
			if err := tx.updateNode(ctx, &node, status, nil); err != nil {
				return err
			}
			outcome.Outcome, outcome.Reason = "accepted", ""
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO completion_dispositions(attempt_id, event_id, payload_digest, disposition, reason, created_at)
			VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)
		`, attemptID, eventID, digest, outcome.Outcome, outcome.Reason, tx.now); err != nil {
			return err
		}
		data := struct {
			Disposition Disposition `json:"disposition"`
			Node        Node        `json:"node"`
		}{outcome, node}
		return tx.event(ctx, node.RunID, &nodeID, &attemptID, "completion_recorded", data)
	})
	if err != nil {
		return Disposition{}, err
	}
	return outcome, nil
}
