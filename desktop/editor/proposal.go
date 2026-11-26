package editor

import (
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/improve"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ProposalReview coordinates reviewing, toggling, diffing, and saving
// an evidence-backed workflow improvement proposal.
type ProposalReview struct {
	BaseYAML    string
	CurrentYAML string
	Proposal    improve.Proposal
	Original    TemplatePreview
	Result      *improve.ApplyResult
}

// NewProposalReview initializes a ProposalReview from a compiled definition and proposal.
func NewProposalReview(cw *workflow.CompiledWorkflow, baseYAML, currentYAML string, prop improve.Proposal) ProposalReview {
	return ProposalReview{
		BaseYAML:    baseYAML,
		CurrentYAML: currentYAML,
		Proposal:    prop,
		Original:    BuildTemplatePreview(cw, nil),
	}
}

// SetAccepted toggles acceptance of changeID.
func (r *ProposalReview) SetAccepted(changeID string, accepted bool) {
	for i := range r.Proposal.Changes {
		if r.Proposal.Changes[i].ID == changeID {
			r.Proposal.Changes[i].Accepted = accepted
		}
	}
}

// Preview computes the merged YAML and diff for the currently accepted changes.
func (r *ProposalReview) Preview(reg *catalog.Registry) (improve.ApplyResult, error) {
	res, err := improve.ApplyProposal(r.BaseYAML, r.CurrentYAML, r.Proposal, reg)
	if err != nil {
		return res, err
	}
	r.Result = &res
	return res, nil
}
