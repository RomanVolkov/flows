package improve_test

import (
	"strings"
	"testing"

	"github.com/romanvolkov/flows/internal/activity"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/improve"
)

func TestCollectEvidenceRedactionExcerptsAndProposalValidation(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()

	bundle := improve.CollectEvidence(improve.CollectRequest{
		RunID:            "run-99",
		BaseRevisionID:   "rev-base-1",
		BaseWorkflowYAML: "schema: flows/v1\nid: demo\nversion: 1.0.0\nnodes:\n  check:\n    uses: core.harness\n",
		Turns: []harness.Turn{
			{Role: "user", Text: "run linter with ghp_secret123"},
			{Role: "user", Text: "hidden-pw", Hidden: true},
		},
		Excerpts: []activity.TerminalExcerpt{
			{AttemptID: "att-1", StartSeq: 1, EndSeq: 20, Text: "user: check FLOWS_ATTEMPT_TOKEN=tok-xyz", CoverageGap: "unstructured_tui"},
			{AttemptID: "att-1", StartSeq: 21, EndSeq: 40, Text: "assistant: lint passed"},
		},
		Secrets: []string{"ghp_secret123"},
	})

	if !bundle.Availability.BaseRevision || !bundle.Availability.StructuredTurns || !bundle.Availability.TerminalExcerpts {
		t.Fatalf("unexpected availability: %+v", bundle.Availability)
	}
	if len(bundle.Turns) != 1 || strings.Contains(bundle.Turns[0].Text, "ghp_secret123") {
		t.Fatalf("secret or hidden turn leaked: %+v", bundle.Turns)
	}
	if strings.Contains(bundle.SelectedExcerpts[0].Text, "tok-xyz") {
		t.Fatalf("attempt token leaked in excerpt: %+v", bundle.SelectedExcerpts[0])
	}

	// Select only excerpt 1.
	bundle.SelectExcerpts([]int{1})
	if len(bundle.SelectedExcerpts) != 1 || bundle.SelectedExcerpts[0].Text != "assistant: lint passed" {
		t.Fatalf("SelectExcerpts failed: %+v", bundle.SelectedExcerpts)
	}

	// Proposal referencing uninstalled tool must fail; installed tool with evidence passes.
	badProp := improve.Proposal{
		RunID:          "run-99",
		BaseRevisionID: bundle.BaseRevisionID,
		Changes: []improve.ProposedChange{
			{ID: "c1", Kind: improve.ChangeAddCatalogStep, TargetNodeID: "lint", OperationID: "nonexistent.lint", EvidenceLinks: []string{"excerpt:0"}},
		},
	}
	if err := improve.ValidateProposal(badProp, reg); err == nil {
		t.Fatalf("expected uninstalled operation to be rejected")
	}

	goodProp := improve.Proposal{
		RunID:          "run-99",
		BaseRevisionID: bundle.BaseRevisionID,
		Changes: []improve.ProposedChange{
			{ID: "c1", Kind: improve.ChangeAddCatalogStep, TargetNodeID: "lint", OperationID: "core.command", EvidenceLinks: []string{"excerpt:0"}},
		},
	}
	if err := improve.ValidateProposal(goodProp, reg); err != nil {
		t.Fatalf("ValidateProposal failed: %v", err)
	}
}
