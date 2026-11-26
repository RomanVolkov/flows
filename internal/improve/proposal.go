package improve

import (
	"fmt"

	"github.com/romanvolkov/flows/internal/catalog"
)

// ChangeKind classifies a scoped workflow improvement.
type ChangeKind string

const (
	ChangeAddCatalogStep ChangeKind = "add_catalog_step"
	ChangeFixBinding     ChangeKind = "fix_binding"
	ChangeUpdatePrompt   ChangeKind = "update_prompt"
)

// ProposedChange is one individually acceptable workflow edit with evidence links.
type ProposedChange struct {
	ID            string     `json:"id"`
	Kind          ChangeKind `json:"kind"`
	TargetNodeID  string     `json:"target_node_id,omitempty"`
	OperationID   string     `json:"operation_id,omitempty"`
	BindingKey    string     `json:"binding_key,omitempty"`
	BindingValue  string     `json:"binding_value,omitempty"`
	PromptPath    string     `json:"prompt_path,omitempty"`
	PromptContent string     `json:"prompt_content,omitempty"`
	Summary       string     `json:"summary"`
	EvidenceLinks []string   `json:"evidence_links"`
	Accepted      bool       `json:"accepted"`
}

// Proposal is the full improvement patch returned for user review.
type Proposal struct {
	RunID             string           `json:"run_id"`
	BaseRevisionID    string           `json:"base_revision_id"`
	Changes           []ProposedChange `json:"changes"`
	PluginSuggestions []string         `json:"plugin_suggestions,omitempty"`
}

// ValidateProposal ensures every proposed change cites evidence and only
// references operations present in the installed catalog.
func ValidateProposal(p Proposal, reg *catalog.Registry) error {
	if p.BaseRevisionID == "" {
		return fmt.Errorf("proposal missing base_revision_id")
	}
	for _, ch := range p.Changes {
		if len(ch.EvidenceLinks) == 0 {
			return fmt.Errorf("change %q has no evidence links", ch.ID)
		}
		if ch.Kind == ChangeAddCatalogStep {
			if ch.OperationID == "" {
				return fmt.Errorf("change %q missing operation_id", ch.ID)
			}
			if reg != nil {
				if _, _, err := reg.Describe(ch.OperationID); err != nil {
					return fmt.Errorf("change %q references uninstalled operation %q: %w", ch.ID, ch.OperationID, err)
				}
			}
		}
	}
	return nil
}
