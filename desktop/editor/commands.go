package editor

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

// VisualEditor manages round-trip visual editing of a workflow YAML AST.
type VisualEditor struct {
	FilePath         string
	BaseDigest       string
	OriginalYAML     string
	Definition       *workflow.Definition
	Diagnostics      []workflow.Diagnostic
	ConflictExternal bool
}

// OpenWorkflowFile loads path, parses its preserved AST, and validates against reg.
func OpenWorkflowFile(path string, reg *catalog.Registry) (*VisualEditor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	def, pDiags := workflow.ParseBytes(path, data)
	ed := &VisualEditor{
		FilePath:     path,
		BaseDigest:   workflow.DigestBytes(data),
		OriginalYAML: string(data),
		Definition:   def,
		Diagnostics:  pDiags,
	}
	if len(pDiags) == 0 && reg != nil {
		_, cDiags := workflow.Compile(def, reg, nil)
		ed.Diagnostics = cDiags
	}
	return ed, nil
}

// NewWorkflow creates a fresh in-editor workflow definition ready for visual edits.
func NewWorkflow(path, id string) (*VisualEditor, error) {
	seed := fmt.Sprintf("schema: flows/v1\nid: %s\nversion: 1.0.0\nnodes:\n  start:\n    uses: core.command\n", id)
	def, diags := workflow.ParseBytes(path, []byte(seed))
	if len(diags) > 0 {
		return nil, diags[0]
	}
	return &VisualEditor{
		FilePath:     path,
		OriginalYAML: seed,
		Definition:   def,
	}, nil
}

// AddNode inserts a new node into the preserved YAML syntax tree.
func (e *VisualEditor) AddNode(nodeID, uses string) error {
	root := e.rootMapping()
	if root == nil {
		return fmt.Errorf("missing root mapping")
	}
	nodesMap := workflow.EnsureMappingChild(root, "nodes")
	nodeMap := workflow.EnsureMappingChild(nodesMap, nodeID)
	workflow.UpsertMappingScalar(nodeMap, "uses", uses)
	return e.reparse()
}

// AddDependency adds a needs edge onto nodeID in the preserved YAML syntax tree.
func (e *VisualEditor) AddDependency(nodeID, fromNodeID string) error {
	root := e.rootMapping()
	if root == nil {
		return fmt.Errorf("missing root mapping")
	}
	nodesMap := workflow.EnsureMappingChild(root, "nodes")
	nodeMap := workflow.EnsureMappingChild(nodesMap, nodeID)
	workflow.AppendSequenceScalar(nodeMap, "needs", fromNodeID)
	return e.reparse()
}

// SetWithBinding sets a `with` input binding on nodeID.
func (e *VisualEditor) SetWithBinding(nodeID, key, value string) error {
	root := e.rootMapping()
	if root == nil {
		return fmt.Errorf("missing root mapping")
	}
	nodesMap := workflow.EnsureMappingChild(root, "nodes")
	nodeMap := workflow.EnsureMappingChild(nodesMap, nodeID)
	withMap := workflow.EnsureMappingChild(nodeMap, "with")
	workflow.UpsertMappingScalar(withMap, key, value)
	return e.reparse()
}

// PreviewDiff returns a human-readable YAML diff between the original file and current AST.
func (e *VisualEditor) PreviewDiff() (string, error) {
	cur, err := workflow.EncodeSyntaxTree(e.Definition.SyntaxTree)
	if err != nil {
		return "", err
	}
	return workflow.LineDiff(e.OriginalYAML, string(cur)), nil
}

// Save validates and atomically writes the edited YAML, detecting external edits.
func (e *VisualEditor) Save(reg *catalog.Registry, forceOverwrite bool) error {
	cur, err := workflow.EncodeSyntaxTree(e.Definition.SyntaxTree)
	if err != nil {
		return err
	}
	base := e.BaseDigest
	if forceOverwrite {
		base = ""
	}
	res, err := workflow.SaveDefinitionAtomic(e.FilePath, base, cur, reg, nil)
	if err != nil {
		if errors.Is(err, workflow.ErrExternalEdit) {
			e.ConflictExternal = true
		}
		return err
	}
	e.BaseDigest = res.Digest
	e.OriginalYAML = string(cur)
	e.ConflictExternal = false
	return nil
}

func (e *VisualEditor) rootMapping() *yaml.Node {
	if e.Definition == nil || e.Definition.SyntaxTree == nil || len(e.Definition.SyntaxTree.Content) == 0 {
		return nil
	}
	return e.Definition.SyntaxTree.Content[0]
}

func (e *VisualEditor) reparse() error {
	raw, err := workflow.EncodeSyntaxTree(e.Definition.SyntaxTree)
	if err != nil {
		return err
	}
	def, diags := workflow.ParseBytes(e.FilePath, raw)
	e.Definition = def
	e.Diagnostics = diags
	return nil
}
