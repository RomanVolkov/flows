package inspector

import (
	"fmt"
	"slices"

	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

// NodeInspector holds the full inspection view for a selected node.
type NodeInspector struct {
	NodeID             string               `json:"node_id"`
	RunID              string               `json:"run_id"`
	AttemptID          string               `json:"attempt_id,omitempty"`
	SessionID          string               `json:"session_id,omitempty"`
	CopyablePath       string               `json:"copyable_path,omitempty"`
	Inputs             map[string]string    `json:"inputs,omitempty"`
	Edges              []store.Edge         `json:"edges,omitempty"`
	WaitReason         *store.WaitReason    `json:"wait_reason,omitempty"`
	WaitLabel          string               `json:"wait_label,omitempty"`
	Attempts           []store.Attempt      `json:"attempts,omitempty"`
	TerminalPreview    string               `json:"terminal_preview,omitempty"`
	CanStop            bool                 `json:"can_stop"`
	CanResume          bool                 `json:"can_resume"`
	NotificationTarget string               `json:"notification_target,omitempty"`
	CriticalStateText  string               `json:"critical_state_text,omitempty"`
	LeaseStatus        host.LeaseStatus     `json:"lease_status"`
}

// BuildNodeInspector constructs an inspector model for node.
func BuildNodeInspector(node store.Node, workDir string, lease host.LeaseStatus, edges []store.Edge, attempts []store.Attempt, preview string) NodeInspector {
	insp := NodeInspector{
		NodeID:          node.ID,
		RunID:           node.RunID,
		CopyablePath:    workDir,
		Edges:           edges,
		WaitReason:      node.Wait,
		Attempts:        attempts,
		TerminalPreview: preview,
		LeaseStatus:     lease,
	}
	if node.AttemptID != nil {
		insp.AttemptID = *node.AttemptID
	}
	if node.Wait != nil {
		insp.WaitLabel = formatWaitLabel(node.Wait)
	}
	switch node.Status {
	case store.Running, store.WaitingForInput:
		insp.CanStop = true
	case store.Interrupted, store.Failed:
		insp.CriticalStateText = fmt.Sprintf("CRITICAL: %s", node.Status)
		insp.CanResume = lease == host.LeaseReleased
	}
	return insp
}

func formatWaitLabel(w *store.WaitReason) string {
	if w == nil {
		return ""
	}
	switch w.Kind {
	case "edge":
		return "Waiting for " + w.Ref
	case "timer":
		return "Waiting for timer"
	case "human":
		return "Waiting for input: " + w.Ref
	default:
		return "Waiting"
	}
}

// OpenChat reads saved conversation history without ever starting a retry,
// even when the node is interrupted with an unresolved checkout lease.
func (n NodeInspector) OpenChat(ts *harness.TranscriptStore) ([]harness.Turn, harness.TranscriptMeta, error) {
	if ts == nil {
		return nil, harness.TranscriptMeta{}, nil
	}
	return ts.Read()
}

// CanContinueChat reports whether starting a new terminal session is eligible.
func (n NodeInspector) CanContinueChat(status store.Status) bool {
	if n.LeaseStatus != host.LeaseReleased {
		return false
	}
	switch status {
	case store.Interrupted, store.Failed, store.Cancelled, store.Succeeded:
		return true
	default:
		return false
	}
}

// DistinctRoots returns unique run IDs from a multi-node selection so batch
// operations act on distinct roots once.
func DistinctRoots(selected []NodeSummary) []string {
	seen := make(map[string]bool, len(selected))
	var roots []string
	for _, s := range selected {
		if s.RunID == "" || seen[s.RunID] {
			continue
		}
		seen[s.RunID] = true
		roots = append(roots, s.RunID)
	}
	slices.Sort(roots)
	return roots
}
