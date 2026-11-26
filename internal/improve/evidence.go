// Package improve collects redacted run evidence and validates/applies
// evidence-backed workflow improvement proposals.
package improve

import (
	"strings"

	"github.com/romanvolkov/flows/internal/activity"
	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/workflow"
)

// EvidenceAvailability reports which evidence sources exist for a run.
type EvidenceAvailability struct {
	BaseRevision     bool `json:"base_revision"`
	GraphEdits       bool `json:"graph_edits"`
	DirectCalls      bool `json:"direct_calls"`
	NodeOutcomes     bool `json:"node_outcomes"`
	Answers          bool `json:"answers"`
	StructuredTurns  bool `json:"structured_turns"`
	TerminalExcerpts bool `json:"terminal_excerpts"`
	Artifacts        bool `json:"artifacts"`
}

// CollectRequest supplies raw run artifacts to assemble a redacted EvidenceBundle.
type CollectRequest struct {
	RunID            string
	BaseRevisionID   string
	BaseWorkflowYAML string
	GraphEdits       []engine.GraphEditCommand
	DirectCalls      []activity.Record
	NodeOutcomes     map[string]string
	Answers          []builtin.Question
	Turns            []harness.Turn
	Excerpts         []activity.TerminalExcerpt
	ArtifactRefs     []string
	Secrets          []string
}

// EvidenceBundle is the redacted, selectable evidence passed to the improvement harness.
type EvidenceBundle struct {
	RunID            string                     `json:"run_id"`
	BaseRevisionID   string                     `json:"base_revision_id"`
	BaseWorkflowYAML string                     `json:"base_workflow_yaml"`
	GraphEdits       []engine.GraphEditCommand  `json:"graph_edits,omitempty"`
	DirectCalls      []activity.Record          `json:"direct_calls,omitempty"`
	NodeOutcomes     map[string]string          `json:"node_outcomes,omitempty"`
	Answers          []builtin.Question         `json:"answers,omitempty"`
	Turns            []harness.Turn             `json:"turns,omitempty"`
	SelectedExcerpts []activity.TerminalExcerpt `json:"selected_excerpts,omitempty"`
	ArtifactRefs     []string                   `json:"artifact_refs,omitempty"`
	Availability     EvidenceAvailability       `json:"availability"`
}

// CollectEvidence assembles an EvidenceBundle, redacts secrets/credentials, and
// records which evidence sources are available.
func CollectEvidence(req CollectRequest) EvidenceBundle {
	redact := func(s string) string {
		out := workflow.Redact(s, req.Secrets)
		for _, marker := range []string{"FLOWS_ATTEMPT_TOKEN=", "Authorization: Bearer "} {
			if idx := strings.Index(out, marker); idx >= 0 {
				end := strings.IndexAny(out[idx:], " \n\r\t\"")
				if end < 0 {
					out = out[:idx] + marker + "[REDACTED]"
				} else {
					out = out[:idx] + marker + "[REDACTED]" + out[idx+end:]
				}
			}
		}
		return out
	}

	turns := make([]harness.Turn, 0, len(req.Turns))
	for _, t := range req.Turns {
		if t.Hidden {
			continue
		}
		t.Text = redact(t.Text)
		turns = append(turns, t)
	}

	excerpts := make([]activity.TerminalExcerpt, 0, len(req.Excerpts))
	for _, ex := range req.Excerpts {
		ex.Text = redact(ex.Text)
		excerpts = append(excerpts, ex)
	}

	answers := make([]builtin.Question, 0, len(req.Answers))
	for _, q := range req.Answers {
		q.Prompt = redact(q.Prompt)
		q.Answer = redact(q.Answer)
		answers = append(answers, q)
	}

	return EvidenceBundle{
		RunID:            req.RunID,
		BaseRevisionID:   req.BaseRevisionID,
		BaseWorkflowYAML: redact(req.BaseWorkflowYAML),
		GraphEdits:       req.GraphEdits,
		DirectCalls:      req.DirectCalls,
		NodeOutcomes:     req.NodeOutcomes,
		Answers:          answers,
		Turns:            turns,
		SelectedExcerpts: excerpts,
		ArtifactRefs:     req.ArtifactRefs,
		Availability: EvidenceAvailability{
			BaseRevision:     req.BaseRevisionID != "" && req.BaseWorkflowYAML != "",
			GraphEdits:       len(req.GraphEdits) > 0,
			DirectCalls:      len(req.DirectCalls) > 0,
			NodeOutcomes:     len(req.NodeOutcomes) > 0,
			Answers:          len(answers) > 0,
			StructuredTurns:  len(turns) > 0,
			TerminalExcerpts: len(excerpts) > 0,
			Artifacts:        len(req.ArtifactRefs) > 0,
		},
	}
}

// SelectExcerpts filters SelectedExcerpts to the user-chosen indices.
func (b *EvidenceBundle) SelectExcerpts(indices []int) {
	var kept []activity.TerminalExcerpt
	for _, idx := range indices {
		if idx >= 0 && idx < len(b.SelectedExcerpts) {
			kept = append(kept, b.SelectedExcerpts[idx])
		}
	}
	b.SelectedExcerpts = kept
}
