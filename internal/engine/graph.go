package engine

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/romanvolkov/flows/internal/host"
)

// EditOpKind identifies one operation inside an atomic graph edit batch.
type EditOpKind string

const (
	OpAddNode     EditOpKind = "add_node"
	OpRemoveNode  EditOpKind = "remove_node"
	OpAddEdge     EditOpKind = "add_edge"
	OpRemoveEdge  EditOpKind = "remove_edge"
	OpRewireEdge  EditOpKind = "rewire_edge"
	OpUpdateInput EditOpKind = "update_inputs"
)

// EditOperation is a single mutation in a GraphEditCommand.
type EditOperation struct {
	Op              EditOpKind      `json:"op"`
	Node            *LogicalStep    `json:"node,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	Edge            *DependencyEdge `json:"edge,omitempty"`
	NewEdge         *DependencyEdge `json:"new_edge,omitempty"`
	Inputs          map[string]any  `json:"inputs,omitempty"`
	PackageID       string          `json:"package_id,omitempty"`
	PackageRevision string          `json:"package_revision,omitempty"`
}

// GraphEditCommand is an atomic, revision-checked, idempotent graph edit batch.
type GraphEditCommand struct {
	RunID            string          `json:"run_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Operations       []EditOperation `json:"operations"`
}

// EditConflictError reports a revision conflict or claimed-node mutation.
type EditConflictError struct {
	ExpectedRevision int64  `json:"expected_revision"`
	CurrentRevision  int64  `json:"current_revision"`
	ConflictingNode  string `json:"conflicting_node,omitempty"`
	Reason           string `json:"reason"`
}

func (e *EditConflictError) Error() string {
	return fmt.Sprintf("graph edit conflict (expected rev %d, current %d): %s", e.ExpectedRevision, e.CurrentRevision, e.Reason)
}

// EditResult summarizes the committed batch.
type EditResult struct {
	RunID          string            `json:"run_id"`
	NewRevision    int64             `json:"new_revision"`
	StableNodeIDs  map[string]string `json:"stable_node_ids,omitempty"`
	PinnedPackages map[string]string `json:"pinned_packages,omitempty"`
	RemovedPending []LogicalStep     `json:"removed_pending,omitempty"`
}

// CancelledAttempt records a cancelled attempt in run history.
type CancelledAttempt struct {
	StepID          string           `json:"step_id"`
	AttemptID       string           `json:"attempt_id"`
	InitiatedBy     string           `json:"initiated_by"` // "user" | "harness" | "engine"
	LeaseStatus     host.LeaseStatus `json:"lease_status"`
	HasPartialWork  bool             `json:"has_partial_work"`
	CheckoutRetained bool            `json:"checkout_retained"`
}

// RunGraph holds the transactional graph state for one run.
type RunGraph struct {
	RunID            string
	Revision         int64
	Steps            map[string]*LogicalStep
	Edges            []DependencyEdge
	Claimed          map[string]bool
	RemovedHistory   []LogicalStep
	AppliedKeys      map[string]EditResult
	PinnedPackages   map[string]string
	CancelledHistory []CancelledAttempt
	CheckoutLeases   map[string]host.LeaseStatus
}

// GraphManager coordinates transactional claims, graph edits, and cancellations.
type GraphManager struct {
	mu   sync.Mutex
	runs map[string]*RunGraph
}

// NewGraphManager creates an empty GraphManager.
func NewGraphManager() *GraphManager {
	return &GraphManager{
		runs: make(map[string]*RunGraph),
	}
}

// InitRun initializes a run graph with initial steps and edges.
func (m *GraphManager) InitRun(runID string, steps []LogicalStep, edges []DependencyEdge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rg := &RunGraph{
		RunID:          runID,
		Revision:       1,
		Steps:          make(map[string]*LogicalStep, len(steps)),
		Edges:          slices.Clone(edges),
		Claimed:        make(map[string]bool),
		AppliedKeys:    make(map[string]EditResult),
		PinnedPackages: make(map[string]string),
		CheckoutLeases: make(map[string]host.LeaseStatus),
	}
	for i := range steps {
		cp := steps[i]
		rg.Steps[cp.ID] = &cp
	}
	m.runs[runID] = rg
}

// Claim marks stepID as claimed under the same lock used by ApplyEdit,
// preventing late dependency edits from racing execution dispatch.
func (m *GraphManager) Claim(runID, stepID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rg, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("unknown run %q", runID)
	}
	st, ok := rg.Steps[stepID]
	if !ok {
		return fmt.Errorf("unknown step %q", stepID)
	}
	if rg.Claimed[stepID] || st.Status != StepReady {
		return fmt.Errorf("step %q is not claimable (status=%s)", stepID, st.Status)
	}
	rg.Claimed[stepID] = true
	st.Status = StepRunning
	return nil
}

// ApplyEdit validates and applies cmd atomically against expected revision.
func (m *GraphManager) ApplyEdit(cmd GraphEditCommand) (EditResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rg, ok := m.runs[cmd.RunID]
	if !ok {
		return EditResult{}, fmt.Errorf("unknown run %q", cmd.RunID)
	}
	if cmd.IdempotencyKey != "" {
		if prev, exists := rg.AppliedKeys[cmd.IdempotencyKey]; exists {
			return prev, nil
		}
	}
	if cmd.ExpectedRevision != rg.Revision {
		return EditResult{}, &EditConflictError{
			ExpectedRevision: cmd.ExpectedRevision,
			CurrentRevision:  rg.Revision,
			Reason:           "revision mismatch",
		}
	}

	// Stage mutations on a copy so any validation failure rolls back cleanly.
	stagedSteps := make(map[string]*LogicalStep, len(rg.Steps))
	for k, v := range rg.Steps {
		cp := *v
		stagedSteps[k] = &cp
	}
	stagedEdges := slices.Clone(rg.Edges)
	stagedPinned := make(map[string]string, len(rg.PinnedPackages))
	for k, v := range rg.PinnedPackages {
		stagedPinned[k] = v
	}
	stableIDs := make(map[string]string)
	var removedPending []LogicalStep

	for _, op := range cmd.Operations {
		if op.PackageID != "" && op.PackageRevision != "" {
			stagedPinned[op.PackageID] = op.PackageRevision
		}
		switch op.Op {
		case OpAddNode:
			if op.Node == nil || op.Node.ID == "" {
				return EditResult{}, errors.New("add_node requires node with id")
			}
			if op.Node.RunID != "" && op.Node.RunID != cmd.RunID {
				return EditResult{}, fmt.Errorf("node %q belongs to run %q, expected %q", op.Node.ID, op.Node.RunID, cmd.RunID)
			}
			if _, exists := stagedSteps[op.Node.ID]; exists {
				return EditResult{}, fmt.Errorf("node %q already exists", op.Node.ID)
			}
			cp := *op.Node
			cp.RunID = cmd.RunID
			if cp.Status == "" {
				cp.Status = StepWaiting
			}
			stagedSteps[cp.ID] = &cp
			stableIDs[cp.ID] = cp.ID

		case OpRemoveNode:
			st, exists := stagedSteps[op.NodeID]
			if !exists {
				return EditResult{}, fmt.Errorf("remove_node: unknown node %q", op.NodeID)
			}
			if rg.Claimed[op.NodeID] || (st.Status != StepWaiting && st.Status != StepReady) {
				return EditResult{}, &EditConflictError{
					ExpectedRevision: cmd.ExpectedRevision,
					CurrentRevision:  rg.Revision,
					ConflictingNode:  op.NodeID,
					Reason:           "cannot remove claimed or active node",
				}
			}
			removedPending = append(removedPending, *st)
			delete(stagedSteps, op.NodeID)
			stagedEdges = slices.DeleteFunc(stagedEdges, func(e DependencyEdge) bool {
				return e.FromStepID == op.NodeID || e.ToStepID == op.NodeID
			})

		case OpAddEdge:
			if op.Edge == nil {
				return EditResult{}, errors.New("add_edge requires edge")
			}
			if _, ok := stagedSteps[op.Edge.FromStepID]; !ok {
				return EditResult{}, fmt.Errorf("add_edge: unknown from_step_id %q", op.Edge.FromStepID)
			}
			if _, ok := stagedSteps[op.Edge.ToStepID]; !ok {
				return EditResult{}, fmt.Errorf("add_edge: unknown to_step_id %q", op.Edge.ToStepID)
			}
			if rg.Claimed[op.Edge.ToStepID] {
				return EditResult{}, &EditConflictError{
					ExpectedRevision: cmd.ExpectedRevision,
					CurrentRevision:  rg.Revision,
					ConflictingNode:  op.Edge.ToStepID,
					Reason:           "cannot add dependency to already-claimed node",
				}
			}
			stagedEdges = append(stagedEdges, *op.Edge)

		case OpRemoveEdge:
			if op.Edge == nil {
				return EditResult{}, errors.New("remove_edge requires edge")
			}
			if rg.Claimed[op.Edge.ToStepID] {
				return EditResult{}, &EditConflictError{
					ExpectedRevision: cmd.ExpectedRevision,
					CurrentRevision:  rg.Revision,
					ConflictingNode:  op.Edge.ToStepID,
					Reason:           "cannot mutate dependencies of already-claimed node",
				}
			}
			stagedEdges = slices.DeleteFunc(stagedEdges, func(e DependencyEdge) bool {
				return e == *op.Edge
			})

		case OpRewireEdge:
			if op.Edge == nil || op.NewEdge == nil {
				return EditResult{}, errors.New("rewire_edge requires edge and new_edge")
			}
			if rg.Claimed[op.Edge.ToStepID] || rg.Claimed[op.NewEdge.ToStepID] {
				return EditResult{}, &EditConflictError{
					ExpectedRevision: cmd.ExpectedRevision,
					CurrentRevision:  rg.Revision,
					ConflictingNode:  op.NewEdge.ToStepID,
					Reason:           "cannot rewire dependencies of already-claimed node",
				}
			}
			stagedEdges = slices.DeleteFunc(stagedEdges, func(e DependencyEdge) bool {
				return e == *op.Edge
			})
			stagedEdges = append(stagedEdges, *op.NewEdge)

		case OpUpdateInput:
			if rg.Claimed[op.NodeID] {
				return EditResult{}, &EditConflictError{
					ExpectedRevision: cmd.ExpectedRevision,
					CurrentRevision:  rg.Revision,
					ConflictingNode:  op.NodeID,
					Reason:           "cannot mutate inputs of already-claimed node",
				}
			}
			st, exists := stagedSteps[op.NodeID]
			if !exists {
				return EditResult{}, fmt.Errorf("update_inputs: unknown node %q", op.NodeID)
			}
			if st.Outputs == nil {
				st.Outputs = make(map[string]any)
			}
			for k, v := range op.Inputs {
				st.Outputs[k] = v
			}

		default:
			return EditResult{}, fmt.Errorf("unknown edit operation %q", op.Op)
		}
	}

	if cyc := detectStepCycle(stagedSteps, stagedEdges); len(cyc) > 0 {
		return EditResult{}, fmt.Errorf("graph edit introduces dependency cycle: %v", cyc)
	}

	rg.Steps = stagedSteps
	rg.Edges = stagedEdges
	rg.PinnedPackages = stagedPinned
	rg.RemovedHistory = append(rg.RemovedHistory, removedPending...)
	rg.Revision++

	res := EditResult{
		RunID:          cmd.RunID,
		NewRevision:    rg.Revision,
		StableNodeIDs:  stableIDs,
		PinnedPackages: stagedPinned,
		RemovedPending: removedPending,
	}
	if cmd.IdempotencyKey != "" {
		rg.AppliedKeys[cmd.IdempotencyKey] = res
	}
	return res, nil
}

// CancelStep cancels stepID (initiated by user, harness, or engine), attaches
// the cancelled attempt to history, and retains the checkout lease if the
// previous writer cannot be confirmed stopped.
func (m *GraphManager) CancelStep(runID, stepID, attemptID, initiatedBy, checkoutKey string, writer *host.ProcessIdentity, hasPartialWork bool) (CancelledAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rg, ok := m.runs[runID]
	if !ok {
		return CancelledAttempt{}, fmt.Errorf("unknown run %q", runID)
	}
	st, ok := rg.Steps[stepID]
	if !ok {
		return CancelledAttempt{}, fmt.Errorf("unknown step %q", stepID)
	}

	// Idempotent repeat cancellation.
	if st.Status == StepCancelled {
		for _, ca := range rg.CancelledHistory {
			if ca.StepID == stepID && ca.AttemptID == attemptID {
				return ca, nil
			}
		}
	}

	lease := host.LeaseReleased
	if writer != nil && host.VerifyProcess(*writer) == nil {
		lease = host.LeaseUnresolved
	}
	if checkoutKey != "" {
		rg.CheckoutLeases[checkoutKey] = lease
	}
	st.Status = StepCancelled
	ca := CancelledAttempt{
		StepID:           stepID,
		AttemptID:        attemptID,
		InitiatedBy:      initiatedBy,
		LeaseStatus:      lease,
		HasPartialWork:   hasPartialWork,
		CheckoutRetained: hasPartialWork || lease != host.LeaseReleased,
	}
	rg.CancelledHistory = append(rg.CancelledHistory, ca)
	return ca, nil
}

// RunSnapshot returns a copy of the run's current revision and history.
func (m *GraphManager) RunSnapshot(runID string) (*RunGraph, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rg, ok := m.runs[runID]
	return rg, ok
}

func detectStepCycle(steps map[string]*LogicalStep, edges []DependencyEdge) []string {
	adj := make(map[string][]string, len(steps))
	for _, e := range edges {
		adj[e.FromStepID] = append(adj[e.FromStepID], e.ToStepID)
	}
	var ids []string
	for id := range steps {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	state := make(map[string]int, len(ids))
	var path []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		state[u] = 1
		path = append(path, u)
		for _, v := range adj[u] {
			if state[v] == 1 {
				return append(append([]string(nil), path...), v)
			}
			if state[v] == 0 {
				if c := dfs(v); len(c) > 0 {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		state[u] = 2
		return nil
	}
	for _, id := range ids {
		if state[id] == 0 {
			if c := dfs(id); len(c) > 0 {
				return c
			}
		}
	}
	return nil
}
