package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const workflowUnknownOutcome = "The daemon stopped while this operation was running. Its outcome is unknown."

type WorkflowRun struct {
	RunID   string          `json:"run_id"`
	NodeID  string          `json:"node_id"`
	Status  Status          `json:"status"`
	Outputs json.RawMessage `json:"outputs,omitempty"`
}

type WorkflowStep struct {
	NodeID, StepID, Kind                         string
	ToolID, CacheKey, BinaryDigest, InvocationID string
}

type WorkflowEdge struct {
	From, To, Milestone string
}

type NewWorkflowRun struct {
	RequestID, WorkflowID, Digest, SnapshotDigest, PackageDir, WorkDir string
	Inputs, Snapshot                                                   json.RawMessage
	Steps                                                              []WorkflowStep
	Edges                                                              []WorkflowEdge
}

func (s *Store) WorkflowRunByRequest(ctx context.Context, requestID string) (WorkflowRun, error) {
	return workflowRunByRequest(ctx, s.db, requestID)
}

func workflowRunByRequest(ctx context.Context, db queryer, requestID string) (WorkflowRun, error) {
	var run WorkflowRun
	var outputs sql.NullString
	err := db.QueryRowContext(ctx, `SELECT w.run_id,w.root_node_id,w.status,w.outputs_json
		FROM workflow_runs w JOIN run_roots r ON w.run_id=r.id WHERE r.request_id=?`, requestID).
		Scan(&run.RunID, &run.NodeID, &run.Status, &outputs)
	run.Outputs = json.RawMessage(outputs.String)
	return run, err
}

func (s *Store) CreateWorkflowRun(ctx context.Context, spec NewWorkflowRun) (WorkflowRun, bool, error) {
	var run WorkflowRun
	created := false
	err := s.write(ctx, func(tx *writeTx) error {
		var err error
		run, err = workflowRunByRequest(ctx, tx, spec.RequestID)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO definition_revisions(id,definition_id,digest,compiled_json,created_at)
			VALUES(?,?,?,?,?) ON CONFLICT(definition_id,digest) DO NOTHING`, rand.Text(), spec.WorkflowID,
			spec.SnapshotDigest, string(spec.Snapshot), tx.now); err != nil {
			return err
		}
		var revision string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM definition_revisions WHERE definition_id=? AND digest=?", spec.WorkflowID, spec.SnapshotDigest).Scan(&revision); err != nil {
			return err
		}
		run = WorkflowRun{RunID: rand.Text(), NodeID: rand.Text(), Status: Ready}
		if _, err := tx.ExecContext(ctx, "INSERT INTO run_roots(id,definition_revision_id,request_id,created_at) VALUES(?,?,?,?)", run.RunID, revision, spec.RequestID, tx.now); err != nil {
			return err
		}
		if err := tx.event(ctx, run.RunID, nil, nil, "run_created", struct{}{}); err != nil {
			return err
		}
		root := Node{ID: run.NodeID, RunID: run.RunID, Kind: "group", Name: spec.WorkflowID, Status: Ready}
		if err := tx.insertWorkflowNode(ctx, root); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_runs(run_id,root_node_id,definition_revision_id,workflow_id,workflow_digest,package_dir,work_dir,inputs_json,snapshot_json,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, run.RunID, run.NodeID, revision, spec.WorkflowID, spec.Digest, spec.PackageDir, spec.WorkDir,
			string(spec.Inputs), string(spec.Snapshot), run.Status, tx.now, tx.now); err != nil {
			return err
		}
		waiting := make(map[string]string)
		for _, edge := range spec.Edges {
			waiting[edge.To] = edge.From
		}
		for _, step := range spec.Steps {
			node := Node{ID: step.NodeID, RunID: run.RunID, ParentID: &run.NodeID, Kind: step.Kind, Name: step.StepID, Status: Ready}
			if dep := waiting[step.StepID]; dep != "" {
				node.Status, node.Wait = WaitingForDependencies, &WaitReason{Kind: "edge", Ref: dep}
			}
			if err := tx.insertWorkflowNode(ctx, node); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_steps(node_id,run_id,parent_node_id,step_key,step_id,node_type,pinned_tool_id,pinned_cache_key,pinned_binary_digest,invocation_id,created_at,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, node.ID, run.RunID, run.NodeID, step.StepID, step.StepID, step.Kind, step.ToolID,
				step.CacheKey, step.BinaryDigest, step.InvocationID, tx.now, tx.now); err != nil {
				return err
			}
		}
		ids := make(map[string]string, len(spec.Steps))
		for _, step := range spec.Steps {
			ids[step.StepID] = step.NodeID
		}
		for _, dependency := range spec.Edges {
			edge := Edge{ID: rand.Text(), RunID: run.RunID, FromID: ids[dependency.From], ToID: ids[dependency.To], Milestone: dependency.Milestone}
			if _, err := tx.ExecContext(ctx, "INSERT INTO edges(id,run_id,from_id,to_id,milestone) VALUES(?,?,?,?,?)", edge.ID, run.RunID, edge.FromID, edge.ToID, edge.Milestone); err != nil {
				return err
			}
			if err := tx.event(ctx, run.RunID, &edge.ToID, nil, "edge_added", edge); err != nil {
				return err
			}
		}
		created = true
		return tx.advanceGraph(ctx, run.RunID)
	})
	return run, created && err == nil, err
}

func (tx *writeTx) insertWorkflowNode(ctx context.Context, node Node) error {
	node.CreatedAt, node.UpdatedAt = tx.now, tx.now
	var kind, ref *string
	if node.Wait != nil {
		kind, ref = &node.Wait.Kind, &node.Wait.Ref
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO nodes(id,run_id,parent_id,kind,name,status,wait_kind,wait_ref,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, node.ID, node.RunID, node.ParentID, node.Kind, node.Name, node.Status, kind, ref, tx.now, tx.now); err != nil {
		return err
	}
	return tx.event(ctx, node.RunID, &node.ID, nil, "node_added", node)
}

func (s *Store) MarkWorkflowStepInFlight(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.ExecContext(ctx, "UPDATE workflow_steps SET in_flight_unknown=1,updated_at=? WHERE node_id=?", tx.now, id)
		return err
	})
}

func (s *Store) FinishWorkflowNode(ctx context.Context, nodeID string, status Status, outputs map[string]any, message string) error {
	data, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *writeTx) error {
		node, err := readNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if node.Status != Running && node.Status != Stopping && !(status == Cancelled || status == Skipped) {
			return fmt.Errorf("%w: workflow node %s from %s", ErrTransition, nodeID, node.Status)
		}
		if err := tx.updateNode(ctx, &node, status, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE workflow_steps SET outputs_json=?,in_flight_unknown=0,updated_at=? WHERE node_id=?", string(data), tx.now, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE workflow_runs SET outputs_json=?,status=?,error=?,updated_at=? WHERE root_node_id=?", string(data), status, message, tx.now, nodeID); err != nil {
			return err
		}
		return tx.event(ctx, node.RunID, &node.ID, node.AttemptID, "node_status_changed", node)
	})
}

func (s *Store) MarkUnfinishedRunsInterrupted(ctx context.Context) error {
	return s.write(ctx, func(tx *writeTx) error {
		nodes, err := collect(ctx, tx.Tx, "SELECT "+nodeColumns+" FROM nodes WHERE run_id IN (SELECT run_id FROM workflow_runs WHERE status IN ('ready','running')) AND status IN ('ready','running','waiting_for_dependencies','stopping','waiting_for_input')", scanNode)
		if err != nil {
			return err
		}
		for _, node := range nodes {
			var unknown bool
			err := tx.QueryRowContext(ctx, "SELECT in_flight_unknown FROM workflow_steps WHERE node_id=?", node.ID).Scan(&unknown)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if unknown {
				node.Detail = workflowUnknownOutcome
			}
			if err := tx.updateNode(ctx, &node, Interrupted, nil); err != nil {
				return err
			}
			if err := tx.event(ctx, node.RunID, &node.ID, node.AttemptID, "node_status_changed", node); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE workflow_runs SET status='interrupted',error='daemon restarted before the workflow finished',updated_at=? WHERE status IN ('ready','running')", tx.now)
		return err
	})
}
