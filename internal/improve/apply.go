package improve

import (
	"errors"
	"fmt"
	"strings"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/workflow"
)

var ErrOverlappingRevision = errors.New("accepted proposal overlaps with newer edits on disk")

// ApplyResult holds the merged YAML, prompt updates, diff, and conflict diagnostics.
type ApplyResult struct {
	UpdatedYAML         string            `json:"updated_yaml"`
	PromptUpdates       map[string]string `json:"prompt_updates,omitempty"`
	YAMLDiff            string            `json:"yaml_diff"`
	OverlappingNodes    []string          `json:"overlapping_nodes,omitempty"`
	PreservedNewerNodes []string          `json:"preserved_newer_nodes,omitempty"`
}

// ApplyProposal applies accepted changes onto currentYAML while comparing against
// baseYAML: unrelated newer edits are preserved, overlapping edits are surfaced.
func ApplyProposal(baseYAML, currentYAML string, prop Proposal, reg *catalog.Registry) (ApplyResult, error) {
	if err := ValidateProposal(prop, reg); err != nil {
		return ApplyResult{}, err
	}

	baseDef, bDiags := workflow.ParseBytes("base.yaml", []byte(baseYAML))
	if len(bDiags) > 0 {
		return ApplyResult{}, bDiags[0]
	}
	curDef, cDiags := workflow.ParseBytes("current.yaml", []byte(currentYAML))
	if len(cDiags) > 0 {
		return ApplyResult{}, cDiags[0]
	}

	// Detect nodes added or modified between baseDef and curDef.
	newerModified := make(map[string]bool)
	var preserved []string
	for id, curNode := range curDef.Nodes {
		baseNode, existed := baseDef.Nodes[id]
		if !existed || baseNode.Uses != curNode.Uses || len(baseNode.Needs) != len(curNode.Needs) || len(baseNode.With) != len(curNode.With) {
			newerModified[id] = true
			preserved = append(preserved, id)
		}
	}

	root := curDef.SyntaxTree.Content[0]
	nodesMap := workflow.EnsureMappingChild(root, "nodes")
	promptUpdates := make(map[string]string)
	var overlapping []string

	for _, ch := range prop.Changes {
		if !ch.Accepted {
			continue
		}
		if ch.TargetNodeID != "" && newerModified[ch.TargetNodeID] {
			overlapping = append(overlapping, ch.TargetNodeID)
			continue
		}
		switch ch.Kind {
		case ChangeAddCatalogStep:
			nodeMap := workflow.EnsureMappingChild(nodesMap, ch.TargetNodeID)
			workflow.UpsertMappingScalar(nodeMap, "uses", ch.OperationID)
		case ChangeFixBinding:
			nodeMap := workflow.EnsureMappingChild(nodesMap, ch.TargetNodeID)
			withMap := workflow.EnsureMappingChild(nodeMap, "with")
			workflow.UpsertMappingScalar(withMap, ch.BindingKey, ch.BindingValue)
		case ChangeUpdatePrompt:
			if ch.PromptPath != "" {
				promptUpdates[ch.PromptPath] = ch.PromptContent
			}
		}
	}

	if len(overlapping) > 0 {
		return ApplyResult{
			OverlappingNodes:    overlapping,
			PreservedNewerNodes: preserved,
		}, fmt.Errorf("%w: nodes %v", ErrOverlappingRevision, overlapping)
	}

	updatedBytes, err := workflow.EncodeSyntaxTree(curDef.SyntaxTree)
	if err != nil {
		return ApplyResult{}, err
	}
	mergedDef, mDiags := workflow.ParseBytes("merged.yaml", updatedBytes)
	if len(mDiags) > 0 {
		return ApplyResult{}, mDiags[0]
	}
	if _, compDiags := workflow.Compile(mergedDef, reg, nil); len(compDiags) > 0 {
		return ApplyResult{}, compDiags[0]
	}

	return ApplyResult{
		UpdatedYAML:         string(updatedBytes),
		PromptUpdates:       promptUpdates,
		YAMLDiff:            workflow.LineDiff(currentYAML, string(updatedBytes)),
		PreservedNewerNodes: preserved,
	}, nil
}

// ExtractBranchAsWorkflow converts a live branch into a clean reusable flows/v1
// YAML draft, stripping attempt-specific IDs and exposing incomingParams as inputs.
func ExtractBranchAsWorkflow(newID string, steps []engine.LogicalStep, edges []engine.DependencyEdge, incomingParams []string) (string, error) {
	if newID == "" || len(steps) == 0 {
		return "", fmt.Errorf("newID and at least one step are required")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "schema: flows/v1\nid: %s\nversion: 1.0.0\n", newID)
	if len(incomingParams) > 0 {
		b.WriteString("inputs:\n")
		for _, p := range incomingParams {
			fmt.Fprintf(&b, "  %s: {type: string, required: true}\n", p)
		}
	}
	b.WriteString("nodes:\n")

	cleanID := func(raw string) string {
		if idx := strings.IndexAny(raw, "#["); idx >= 0 {
			raw = raw[:idx]
		}
		return strings.ReplaceAll(raw, ".", "_")
	}

	stepSet := make(map[string]bool, len(steps))
	for _, st := range steps {
		stepSet[st.ID] = true
	}

	var lastID string
	for _, st := range steps {
		cid := cleanID(st.ID)
		lastID = cid
		uses := "core.command"
		if st.Kind == "session" {
			uses = "core.harness"
		}
		fmt.Fprintf(&b, "  %s:\n    uses: %s\n", cid, uses)
		var needs []string
		for _, e := range edges {
			if e.ToStepID == st.ID && stepSet[e.FromStepID] {
				needs = append(needs, cleanID(e.FromStepID))
			}
		}
		if len(needs) > 0 {
			fmt.Fprintf(&b, "    needs: [%s]\n", strings.Join(needs, ", "))
		}
	}
	if lastID != "" {
		fmt.Fprintf(&b, "outputs:\n  result: ${{ nodes.%s.outputs }}\n", lastID)
	}
	return b.String(), nil
}
