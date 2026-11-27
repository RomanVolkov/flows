package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
)

var (
	identRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	exprRe  = regexp.MustCompile(`\$\{\{\s*([^}]+?)\s*\}\}`)
)

// PortSpec describes an exposed output port on a compiled node.
type PortSpec struct {
	CompletionOnly bool                       `json:"completion_only,omitempty"`
	IsArray        bool                       `json:"is_array,omitempty"` // true for for_each fan-out
	SchemaRef      string                     `json:"schema_ref,omitempty"`
	ResolvedSchema json.RawMessage            `json:"resolved_schema,omitempty"`
	Fields         map[string]OutputFieldSpec `json:"fields,omitempty"`
}

// CompiledEdge represents an implicit or explicit dependency between nodes.
type CompiledEdge struct {
	FromID    string `json:"from_id"`
	ToID      string `json:"to_id"`
	Milestone string `json:"milestone,omitempty"`
	Implicit  bool   `json:"implicit,omitempty"`
}

// ExecutionRules records static semantics for fan-out, branches, joins, and keys.
type ExecutionRules struct {
	EmptyFanOut      string `json:"empty_fan_out"`
	SkippedBranch    string `json:"skipped_branch"`
	Join             string `json:"join"`
	DuplicateItemKey string `json:"duplicate_item_key"`
}

// CompiledWorkflow is the validated, catalog-resolved workflow artifact.
type CompiledWorkflow struct {
	Definition *Definition         `json:"definition"`
	Ports      map[string]PortSpec `json:"ports"`
	Edges      []CompiledEdge      `json:"edges"`
	Rules      ExecutionRules      `json:"rules"`
}

// WorkflowLoader resolves nested workflow IDs or relative paths for recursion and type checks.
type WorkflowLoader func(ref string) (*Definition, error)

// Compile runs parse (if needed), TypeCheck, and ResolveCatalog in sequence.
func Compile(def *Definition, reg *catalog.Registry, loader WorkflowLoader) (*CompiledWorkflow, []Diagnostic) {
	if def == nil {
		return nil, []Diagnostic{{Code: "nil_definition", Message: "definition is nil", Severity: "error"}}
	}
	cw, diags := TypeCheck(def)
	if len(diags) > 0 {
		return nil, diags
	}
	catDiags := ResolveCatalog(cw, reg, loader, []string{def.ID})
	if len(catDiags) > 0 {
		return nil, catDiags
	}
	return cw, nil
}

// TypeCheck validates schema version, identifiers, defaults, expressions,
// core.harness output forms, watch/repeat/for_each rules, branches, and cycles.
func TypeCheck(def *Definition) (*CompiledWorkflow, []Diagnostic) {
	var diags []Diagnostic
	addErr := func(field, code, msg string) {
		loc := def.locFor(field)
		diags = append(diags, Diagnostic{
			File:     loc.File,
			Line:     loc.Line,
			Column:   loc.Column,
			Field:    field,
			Code:     code,
			Message:  msg,
			Severity: "error",
		})
	}

	if def.Schema != "flows/v1" {
		addErr("schema", "unsupported_schema", fmt.Sprintf("unsupported schema %q (expected flows/v1)", def.Schema))
	}
	if !identRe.MatchString(def.ID) {
		addErr("id", "invalid_identifier", fmt.Sprintf("invalid workflow id %q", def.ID))
	}
	if strings.TrimSpace(def.Version) == "" {
		addErr("version", "missing_version", "workflow version is required")
	}

	// Validate inputs & defaults.
	for name, in := range def.Inputs {
		if !identRe.MatchString(name) {
			addErr("inputs."+name, "invalid_identifier", fmt.Sprintf("invalid input name %q", name))
		}
		if in.Default != nil && !matchesType(in.Default, in.Type) {
			addErr("inputs."+name+".default", "type_mismatch", fmt.Sprintf("default %v does not match declared type %q", in.Default, in.Type))
		}
	}

	if len(def.Nodes) == 0 {
		addErr("nodes", "empty_nodes", "workflow must declare at least one node")
	}

	ports := make(map[string]PortSpec, len(def.Nodes))
	adj := make(map[string][]string, len(def.Nodes))
	var edges []CompiledEdge

	baseDir := filepath.Dir(def.File)

	// First pass: validate per-node structure and compile output ports.
	for _, nodeID := range def.NodeOrder {
		ns := def.Nodes[nodeID]
		prefix := "nodes." + nodeID
		if !identRe.MatchString(nodeID) {
			addErr(prefix, "invalid_identifier", fmt.Sprintf("invalid node id %q", nodeID))
		}
		if (ns.Uses == "" && ns.Workflow == "") || (ns.Uses != "" && ns.Workflow != "") {
			addErr(prefix, "invalid_target", "node must specify exactly one of uses or workflow")
		}
		if ns.When != "" {
			if strings.EqualFold(strings.TrimSpace(ns.When), "false") || strings.EqualFold(strings.TrimSpace(ns.When), "${{ false }}") {
				addErr(prefix+".when", "unreachable_branch", "branch condition is statically false")
			}
		}
		if ns.ForEach != nil {
			if strings.TrimSpace(ns.ForEach.Items) == "" {
				addErr(prefix+".for_each.items", "missing_binding", "for_each.items is required")
			}
			if strings.TrimSpace(ns.ForEach.Key) == "" || !strings.Contains(ns.ForEach.Key, "item") {
				addErr(prefix+".for_each.key", "invalid_key", "for_each.key must bind an item key expression")
			}
		}
		if ns.Repeat != nil {
			if ns.Repeat.Max <= 0 {
				addErr(prefix+".repeat.max", "invalid_repeat", "repeat.max must be > 0")
			}
		}
		if ns.Watch != nil {
			iv, err := time.ParseDuration(ns.Watch.Interval)
			if err != nil || iv <= 0 {
				addErr(prefix+".watch.interval", "invalid_watch", "watch.interval must be a positive duration")
			}
			dl, err := time.ParseDuration(ns.Watch.Deadline)
			if err != nil || dl <= 0 || (iv > 0 && dl < iv) {
				addErr(prefix+".watch.deadline", "invalid_watch", "watch.deadline must be >= watch.interval")
			}
			if strings.TrimSpace(ns.Watch.Until) == "" {
				addErr(prefix+".watch.until", "invalid_watch", "watch.until predicate is required")
			}
		}

		// Compile output ports (3 forms for core.harness, or standard outputs).
		port := PortSpec{IsArray: ns.ForEach != nil}
		if ns.Outputs == nil {
			port.CompletionOnly = true
		} else if ns.Outputs.Mixed {
			addErr(prefix+".outputs", "mixed_output_forms", "cannot mix $ref and named output fields")
		} else if ns.Outputs.Ref != "" {
			resolved, err := loadLocalSchema(baseDir, ns.Outputs.Ref)
			if err != nil {
				addErr(prefix+".outputs.$ref", "invalid_schema_ref", err.Error())
			} else {
				port.SchemaRef = ns.Outputs.Ref
				port.ResolvedSchema = resolved
			}
		} else {
			port.Fields = ns.Outputs.Fields
		}
		ports[nodeID] = port
	}

	// Second pass: validate explicit needs, implicit ${{ ... }} bindings, and field references.
	for _, nodeID := range def.NodeOrder {
		ns := def.Nodes[nodeID]
		prefix := "nodes." + nodeID

		for _, need := range ns.Needs {
			target, milestone, _ := strings.Cut(need, ".")
			if _, ok := def.Nodes[target]; !ok {
				addErr(prefix+".needs", "unknown_edge", fmt.Sprintf("needs references unknown node %q", target))
				continue
			}
			adj[target] = append(adj[target], nodeID)
			edges = append(edges, CompiledEdge{FromID: target, ToID: nodeID, Milestone: milestone})
		}

		exprSources := collectStrings(ns.With)
		if ns.ForEach != nil {
			exprSources = append(exprSources, ns.ForEach.Items, ns.ForEach.Key)
		}
		if ns.When != "" {
			exprSources = append(exprSources, ns.When)
		}
		for _, exprText := range exprSources {
			for _, ref := range extractExpressions(exprText) {
				validateExprRef(ref, prefix, nodeID, def, ports, ns.ForEach != nil, addErr, func(fromID string) {
					adj[fromID] = append(adj[fromID], nodeID)
					edges = append(edges, CompiledEdge{FromID: fromID, ToID: nodeID, Implicit: true})
				})
			}
		}
	}

	// Validate top-level workflow outputs.
	for outName, exprText := range def.Outputs {
		for _, ref := range extractExpressions(exprText) {
			validateExprRef(ref, "outputs."+outName, "", def, ports, false, addErr, nil)
		}
	}

	// Cycle detection.
	if cycle := detectCycle(def.NodeOrder, adj); len(cycle) > 0 {
		addErr("nodes."+cycle[0], "cycle_detected", fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " -> ")))
	}

	if len(diags) > 0 {
		return nil, diags
	}

	return &CompiledWorkflow{
		Definition: def,
		Ports:      ports,
		Edges:      edges,
		Rules: ExecutionRules{
			EmptyFanOut:      "succeed_empty",
			SkippedBranch:    "propagate_skip",
			Join:             "all_non_skipped",
			DuplicateItemKey: "error",
		},
	}, nil
}

// ResolveCatalog checks operations against reg and nested workflows against loader.
func ResolveCatalog(cw *CompiledWorkflow, reg *catalog.Registry, loader WorkflowLoader, stack []string) []Diagnostic {
	var diags []Diagnostic
	def := cw.Definition
	addErr := func(field, code, msg string) {
		loc := def.locFor(field)
		diags = append(diags, Diagnostic{
			File:     loc.File,
			Line:     loc.Line,
			Column:   loc.Column,
			Field:    field,
			Code:     code,
			Message:  msg,
			Severity: "error",
		})
	}

	for _, nodeID := range def.NodeOrder {
		ns := def.Nodes[nodeID]
		prefix := "nodes." + nodeID
		if ns.Uses != "" {
			if reg == nil {
				continue
			}
			op, _, err := reg.Describe(ns.Uses)
			if err != nil {
				addErr(prefix+".uses", "unknown_operation", fmt.Sprintf("operation %q not found in catalog", ns.Uses))
				continue
			}
			if ns.Watch != nil && !op.ReadOnly {
				addErr(prefix+".watch", "watch_not_readonly", fmt.Sprintf("watch requires a read-only operation, %q is not read-only", ns.Uses))
			}
		} else if ns.Workflow != "" {
			if loader == nil {
				addErr(prefix+".workflow", "unknown_workflow", fmt.Sprintf("nested workflow %q cannot be resolved", ns.Workflow))
				continue
			}
			childDef, err := loader(ns.Workflow)
			if err != nil {
				addErr(prefix+".workflow", "unknown_workflow", err.Error())
				continue
			}
			for _, prev := range stack {
				if prev == childDef.ID {
					addErr(prefix+".workflow", "workflow_recursion", fmt.Sprintf("recursive workflow call %q", childDef.ID))
					return diags
				}
			}
			childCW, childDiags := TypeCheck(childDef)
			if len(childDiags) > 0 {
				diags = append(diags, childDiags...)
				continue
			}
			diags = append(diags, ResolveCatalog(childCW, reg, loader, append(stack, childDef.ID))...)
		}
	}
	return diags
}

func validateExprRef(ref, field, selfNode string, def *Definition, ports map[string]PortSpec, hasItem bool, addErr func(string, string, string), addEdge func(string)) {
	parts := strings.Split(ref, ".")
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "inputs":
		if len(parts) < 2 {
			addErr(field, "invalid_binding", fmt.Sprintf("incomplete input reference %q", ref))
			return
		}
		if _, ok := def.Inputs[parts[1]]; !ok {
			addErr(field, "missing_binding", fmt.Sprintf("unknown input %q", parts[1]))
		}
	case "item":
		if !hasItem {
			addErr(field, "invalid_binding", "item reference used outside for_each")
		}
	case "nodes":
		if len(parts) < 3 || parts[2] != "outputs" {
			addErr(field, "invalid_binding", fmt.Sprintf("invalid node reference %q", ref))
			return
		}
		target := parts[1]
		if target == selfNode {
			addErr(field, "self_reference", fmt.Sprintf("node %q cannot reference its own outputs", selfNode))
			return
		}
		port, ok := ports[target]
		if !ok {
			addErr(field, "unknown_edge", fmt.Sprintf("reference to unknown node %q", target))
			return
		}
		if len(parts) >= 4 && len(port.Fields) > 0 {
			if _, ok := port.Fields[parts[3]]; !ok {
				addErr(field, "undeclared_field", fmt.Sprintf("node %q has no declared output field %q", target, parts[3]))
				return
			}
		}
		if addEdge != nil {
			addEdge(target)
		}
	}
}

func loadLocalSchema(baseDir, ref string) (json.RawMessage, error) {
	if filepath.IsAbs(ref) {
		return nil, fmt.Errorf("schema $ref must be relative: %q", ref)
	}
	clean := filepath.Clean(ref)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("schema $ref escapes package directory: %q", ref)
	}
	full := filepath.Join(baseDir, clean)
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read schema $ref %q: %w", ref, err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("schema $ref %q is not valid JSON", ref)
	}
	return json.RawMessage(data), nil
}

func matchesType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		switch n := v.(type) {
		case int:
			return true
		case float64:
			return !math.IsInf(n, 0) && math.Trunc(n) == n
		default:
			return false
		}
	case "number":
		switch v.(type) {
		case int, float64:
			return true
		default:
			return false
		}
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	default:
		return false
	}
}

func collectStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, x)
	case map[string]any:
		for _, val := range x {
			out = append(out, collectStrings(val)...)
		}
	case []any:
		for _, val := range x {
			out = append(out, collectStrings(val)...)
		}
	}
	return out
}

func extractExpressions(s string) []string {
	matches := exprRe.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 {
			out = append(out, m[1])
		}
	}
	return out
}

func detectCycle(order []string, adj map[string][]string) []string {
	state := make(map[string]int, len(order)) // 0=unvisited, 1=visiting, 2=done
	var path []string
	var dfs func(u string) []string
	dfs = func(u string) []string {
		state[u] = 1
		path = append(path, u)
		for _, v := range adj[u] {
			if state[v] == 1 {
				idx := 0
				for i, p := range path {
					if p == v {
						idx = i
						break
					}
				}
				return append(append([]string(nil), path[idx:]...), v)
			}
			if state[v] == 0 {
				if cyc := dfs(v); len(cyc) > 0 {
					return cyc
				}
			}
		}
		path = path[:len(path)-1]
		state[u] = 2
		return nil
	}
	for _, id := range order {
		if state[id] == 0 {
			if cyc := dfs(id); len(cyc) > 0 {
				return cyc
			}
		}
	}
	return nil
}
