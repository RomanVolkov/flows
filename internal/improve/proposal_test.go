package improve_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/romanvolkov/flows/desktop/editor"
	"github.com/romanvolkov/flows/internal/activity"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/improve"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestPartialAcceptanceOverlappingRevisionBranchExtractionAndFollowUpRun(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()

	baseYAML := `schema: flows/v1
id: improve-demo
version: 1.0.0
nodes:
  review:
    uses: core.harness
`
	// Unrelated newer edit added `notify` node on disk.
	currentYAML := `schema: flows/v1
id: improve-demo
version: 1.0.0
nodes:
  review:
    uses: core.harness
  notify:
    uses: core.notify
`

	// Excerpt-only evidence from an incomplete structured transcript.
	bundle := improve.CollectEvidence(improve.CollectRequest{
		RunID:            "run-1",
		BaseRevisionID:   "rev-1",
		BaseWorkflowYAML: baseYAML,
		Excerpts: []activity.TerminalExcerpt{
			{AttemptID: "att-1", StartSeq: 1, EndSeq: 20, Text: "user: add deterministic lint step", CoverageGap: "no_structured_turns"},
		},
	})

	prop := improve.Proposal{
		RunID:          bundle.RunID,
		BaseRevisionID: bundle.BaseRevisionID,
		Changes: []improve.ProposedChange{
			{
				ID:            "c1",
				Kind:          improve.ChangeAddCatalogStep,
				TargetNodeID:  "lint",
				OperationID:   "core.command",
				Summary:       "Add deterministic lint step",
				EvidenceLinks: []string{"excerpt:0"},
				Accepted:      true,
			},
			{
				ID:            "c2",
				Kind:          improve.ChangeUpdatePrompt,
				TargetNodeID:  "review",
				PromptPath:    "./prompts/review.md",
				PromptContent: "Updated prompt",
				Summary:       "Update review prompt",
				EvidenceLinks: []string{"excerpt:0"},
				Accepted:      false, // partial acceptance: reject c2
			},
		},
	}

	baseDef, _ := workflow.ParseBytes("workflow.yaml", []byte(baseYAML))
	baseCW, _ := workflow.Compile(baseDef, reg, nil)
	review := editor.NewProposalReview(baseCW, baseYAML, currentYAML, prop)

	// 1. Partial acceptance preserves unrelated newer `notify` node.
	res, err := review.Preview(reg)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !strings.Contains(res.UpdatedYAML, "notify:") || !strings.Contains(res.UpdatedYAML, "lint:") {
		t.Fatalf("expected both preserved notify and added lint:\n%s", res.UpdatedYAML)
	}
	if len(res.PromptUpdates) != 0 {
		t.Fatalf("unaccepted prompt update should not be applied")
	}

	// Follow-up run compiles updated definition cleanly.
	updatedDef, pDiags := workflow.ParseBytes("workflow.yaml", []byte(res.UpdatedYAML))
	if len(pDiags) > 0 {
		t.Fatal(pDiags)
	}
	if _, cDiags := workflow.Compile(updatedDef, reg, nil); len(cDiags) > 0 {
		t.Fatalf("follow-up compile failed: %+v", cDiags)
	}

	// 2. Overlapping edit on `notify` surfaces ErrOverlappingRevision.
	propOverlap := improve.Proposal{
		RunID:          "run-1",
		BaseRevisionID: "rev-1",
		Changes: []improve.ProposedChange{
			{ID: "c3", Kind: improve.ChangeFixBinding, TargetNodeID: "notify", BindingKey: "title", BindingValue: "hi", EvidenceLinks: []string{"excerpt:0"}, Accepted: true},
		},
	}
	if _, err := improve.ApplyProposal(baseYAML, currentYAML, propOverlap, reg); !errors.Is(err, improve.ErrOverlappingRevision) {
		t.Fatalf("expected ErrOverlappingRevision, got %v", err)
	}

	// 3. Extract live branch as a new workflow, stripping attempt-specific IDs.
	branchYAML, err := improve.ExtractBranchAsWorkflow("extracted-branch", []engine.LogicalStep{
		{ID: "g1.plan#iter-2", Kind: "session"},
		{ID: "g1.verify[pr-1]", Kind: "tool"},
	}, []engine.DependencyEdge{
		{FromStepID: "g1.plan#iter-2", ToStepID: "g1.verify[pr-1]"},
	}, []string{"repository"})
	if err != nil {
		t.Fatalf("ExtractBranchAsWorkflow: %v", err)
	}
	if strings.Contains(branchYAML, "#iter-2") || strings.Contains(branchYAML, "[pr-1]") || !strings.Contains(branchYAML, "repository:") {
		t.Fatalf("unexpected extracted branch YAML:\n%s", branchYAML)
	}
	bDef, bDiags := workflow.ParseBytes("branch.yaml", []byte(branchYAML))
	if len(bDiags) > 0 {
		t.Fatal(bDiags)
	}
	if _, cDiags := workflow.Compile(bDef, reg, nil); len(cDiags) > 0 {
		t.Fatalf("extracted branch failed compile: %+v", cDiags)
	}
}
