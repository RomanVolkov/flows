package editor

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/romanvolkov/flows/desktop/canvas"
	"github.com/romanvolkov/flows/internal/workflow"
)

// TemplateEdgeKind classifies edges shown on the template canvas.
type TemplateEdgeKind string

const (
	EdgeTypedIO    TemplateEdgeKind = "typed_io"
	EdgeMilestone  TemplateEdgeKind = "milestone"
	EdgeCollection TemplateEdgeKind = "collection"
	EdgeSuccess    TemplateEdgeKind = "success"
)

// TemplateNode describes one declared step on the template canvas.
type TemplateNode struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"` // "harness" | "tool" | "workflow"
	Shape          canvas.ShapeKind `json:"shape"`
	OutputPorts    []string         `json:"output_ports"`
	CompletionOnly bool             `json:"completion_only,omitempty"`
}

// TemplateEdge describes a dependency, milestone, or 0...n collection edge.
type TemplateEdge struct {
	FromID string           `json:"from_id"`
	ToID   string           `json:"to_id"`
	Kind   TemplateEdgeKind `json:"kind"`
	Label  string           `json:"label,omitempty"`
}

// NestedBoundary bounds a nested workflow call on the template canvas.
type NestedBoundary struct {
	NodeID     string `json:"node_id"`
	WorkflowID string `json:"workflow_id"`
	ChildCount int    `json:"child_count"`
}

// TemplatePreview is the laid-out, single-occurrence template representation.
type TemplatePreview struct {
	DefinitionID     string           `json:"definition_id"`
	Nodes            []TemplateNode   `json:"nodes"`
	Edges            []TemplateEdge   `json:"edges"`
	NestedBoundaries []NestedBoundary `json:"nested_boundaries,omitempty"`
	Layout           canvas.WorldLayout `json:"layout"`
}

// BuildTemplatePreview renders each declared step once using the same downward
// canvas geometry, resolving inline and file-backed output ports and 0...n edges.
func BuildTemplatePreview(cw *workflow.CompiledWorkflow, loader workflow.WorkflowLoader) TemplatePreview {
	def := cw.Definition
	preview := TemplatePreview{
		DefinitionID: def.ID,
	}

	var graphNodes []canvas.Node
	var graphEdges []canvas.Edge

	for _, id := range def.NodeOrder {
		ns := def.Nodes[id]
		kind := "tool"
		cKind := canvas.KindTool
		if ns.Uses == "core.harness" {
			kind = "harness"
			cKind = canvas.KindSession
		} else if ns.Workflow != "" {
			kind = "workflow"
			cKind = canvas.KindGroup
			childCount := 0
			if loader != nil {
				if sub, err := loader(ns.Workflow); err == nil {
					childCount = len(sub.Nodes)
				}
			}
			preview.NestedBoundaries = append(preview.NestedBoundaries, NestedBoundary{
				NodeID:     id,
				WorkflowID: ns.Workflow,
				ChildCount: childCount,
			})
		}

		port := cw.Ports[id]
		ports := resolvePortNames(port)
		preview.Nodes = append(preview.Nodes, TemplateNode{
			ID:             id,
			Kind:           kind,
			Shape:          canvas.ShapeForKind(cKind),
			OutputPorts:    ports,
			CompletionOnly: port.CompletionOnly,
		})

		graphNodes = append(graphNodes, canvas.Node{
			ID:     id,
			RunID:  def.ID,
			Kind:   cKind,
			Title:  id,
			Detail: strings.Join(ports, ", "),
			State:  canvas.StatePending,
		})
	}

	for _, e := range cw.Edges {
		ns := def.Nodes[e.ToID]
		ek := EdgeSuccess
		label := ""
		if e.Milestone != "" {
			ek = EdgeMilestone
			label = "@" + e.Milestone
		} else if ns.ForEach != nil {
			ek = EdgeCollection
			label = "0…n"
		} else if !cw.Ports[e.FromID].CompletionOnly {
			ek = EdgeTypedIO
		}
		preview.Edges = append(preview.Edges, TemplateEdge{
			FromID: e.FromID,
			ToID:   e.ToID,
			Kind:   ek,
			Label:  label,
		})
		graphEdges = append(graphEdges, canvas.Edge{
			ID:        e.FromID + "->" + e.ToID,
			RunID:     def.ID,
			FromID:    e.FromID,
			ToID:      e.ToID,
			Kind:      canvas.EdgeDependency,
			Milestone: label,
		})
	}

	preview.Layout = canvas.Layout(canvas.Graph{Nodes: graphNodes, Edges: graphEdges}, canvas.DefaultLayoutConfig(), nil)
	return preview
}

func resolvePortNames(p workflow.PortSpec) []string {
	if p.CompletionOnly {
		return []string{"completion"}
	}
	var names []string
	for k := range p.Fields {
		names = append(names, k)
	}
	if len(names) == 0 && len(p.ResolvedSchema) > 0 {
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if json.Unmarshal(p.ResolvedSchema, &schema) == nil {
			for k := range schema.Properties {
				names = append(names, k)
			}
		}
	}
	slices.Sort(names)
	return names
}
