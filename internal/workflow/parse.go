// Package workflow parses, type-checks, and compiles flows/v1 YAML definitions.
package workflow

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Location identifies a 1-based line and column in a YAML file.
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Diagnostic is a structured, location-aware validation error.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Field    string `json:"field,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s:%d:%d [%s] %s: %s", d.File, d.Line, d.Column, d.Code, d.Field, d.Message)
}

// InputSpec declares a typed workflow input parameter.
type InputSpec struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required,omitempty"`
	Default  any    `yaml:"default" json:"default,omitempty"`
}

// ForEachSpec configures collection fan-out over items with stable key.
type ForEachSpec struct {
	Items string `yaml:"items" json:"items"`
	Key   string `yaml:"key" json:"key"`
}

// RepeatSpec configures bounded repetition.
type RepeatSpec struct {
	Max   int    `yaml:"max" json:"max"`
	Until string `yaml:"until" json:"until,omitempty"`
}

// WatchSpec configures an engine-owned watch poll over a read-only operation.
type WatchSpec struct {
	Interval string `yaml:"interval" json:"interval"`
	Deadline string `yaml:"deadline" json:"deadline"`
	Until    string `yaml:"until" json:"until"`
	Cursor   string `yaml:"cursor" json:"cursor,omitempty"`
}

// OutputFieldSpec declares one typed field in a named output map.
type OutputFieldSpec struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required,omitempty"`
	Items    *struct {
		Type string `yaml:"type" json:"type"`
	} `yaml:"items" json:"items,omitempty"`
}

// HarnessOutputsSpec represents the three core.harness output forms:
// 1. Omitted (nil) -> completion alone
// 2. Named field map (Fields non-empty)
// 3. Standalone local $ref (Ref != "")
type HarnessOutputsSpec struct {
	Ref    string                     `json:"$ref,omitempty"`
	Fields map[string]OutputFieldSpec `json:"fields,omitempty"`
	Mixed  bool                       `json:"-"`
}

// NodeSpec declares one workflow step.
type NodeSpec struct {
	ID       string              `yaml:"-" json:"id"`
	Uses     string              `yaml:"uses" json:"uses,omitempty"`
	Workflow string              `yaml:"workflow" json:"workflow,omitempty"`
	When     string              `yaml:"when" json:"when,omitempty"`
	Needs    []string            `yaml:"needs" json:"needs,omitempty"`
	ForEach  *ForEachSpec        `yaml:"for_each" json:"for_each,omitempty"`
	Repeat   *RepeatSpec         `yaml:"repeat" json:"repeat,omitempty"`
	Watch    *WatchSpec          `yaml:"watch" json:"watch,omitempty"`
	With     map[string]any      `yaml:"with" json:"with,omitempty"`
	Outputs  *HarnessOutputsSpec `yaml:"-" json:"outputs,omitempty"`
}

// Definition is the parsed workflow definition with preserved syntax tree and AST locations.
type Definition struct {
	File       string               `json:"file"`
	Schema     string               `json:"schema"`
	ID         string               `json:"id"`
	Version    string               `json:"version"`
	Inputs     map[string]InputSpec `json:"inputs,omitempty"`
	Milestones []string             `json:"milestones,omitempty"`
	Nodes      map[string]NodeSpec  `json:"nodes"`
	NodeOrder  []string             `json:"node_order"`
	Outputs    map[string]string    `json:"outputs,omitempty"`
	SyntaxTree *yaml.Node           `json:"-"`
	Locations  map[string]Location  `json:"-"`
}

// ParseFile reads and parses a workflow YAML file.
func ParseFile(path string) (*Definition, []Diagnostic) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []Diagnostic{{
			File:     path,
			Line:     1,
			Column:   1,
			Code:     "read_error",
			Message:  err.Error(),
			Severity: "error",
		}}
	}
	return ParseBytes(path, data)
}

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

// ParseBytes parses YAML into a Definition while preserving yaml.Node and AST locations.
func ParseBytes(file string, data []byte) (*Definition, []Diagnostic) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		line := 1
		if m := yamlLineRe.FindStringSubmatch(err.Error()); len(m) == 2 {
			if n, convErr := strconv.Atoi(m[1]); convErr == nil {
				line = n
			}
		}
		return nil, []Diagnostic{{
			File:     file,
			Line:     line,
			Column:   1,
			Code:     "yaml_syntax_error",
			Message:  err.Error(),
			Severity: "error",
		}}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []Diagnostic{{
			File:     file,
			Line:     1,
			Column:   1,
			Code:     "invalid_root",
			Message:  "workflow root must be a YAML mapping",
			Severity: "error",
		}}
	}

	locs := make(map[string]Location)
	collectLocations(file, "", doc.Content[0], locs)

	type rawDef struct {
		Schema     string               `yaml:"schema"`
		ID         string               `yaml:"id"`
		Version    string               `yaml:"version"`
		Inputs     map[string]InputSpec `yaml:"inputs"`
		Milestones []string             `yaml:"milestones"`
		Nodes      yaml.Node            `yaml:"nodes"`
		Outputs    map[string]string    `yaml:"outputs"`
	}
	var raw rawDef
	if err := doc.Content[0].Decode(&raw); err != nil {
		return nil, []Diagnostic{{
			File:     file,
			Line:     1,
			Column:   1,
			Code:     "yaml_decode_error",
			Message:  err.Error(),
			Severity: "error",
		}}
	}

	def := &Definition{
		File:       file,
		Schema:     raw.Schema,
		ID:         raw.ID,
		Version:    raw.Version,
		Inputs:     raw.Inputs,
		Milestones: raw.Milestones,
		Nodes:      make(map[string]NodeSpec),
		Outputs:    raw.Outputs,
		SyntaxTree: &doc,
		Locations:  locs,
	}

	var diags []Diagnostic
	if raw.Nodes.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(raw.Nodes.Content); i += 2 {
			keyNode := raw.Nodes.Content[i]
			valNode := raw.Nodes.Content[i+1]
			nodeID := keyNode.Value
			def.NodeOrder = append(def.NodeOrder, nodeID)

			var ns NodeSpec
			if err := valNode.Decode(&ns); err != nil {
				diags = append(diags, Diagnostic{
					File:     file,
					Line:     valNode.Line,
					Column:   valNode.Column,
					Field:    "nodes." + nodeID,
					Code:     "invalid_node",
					Message:  err.Error(),
					Severity: "error",
				})
				continue
			}
			ns.ID = nodeID
			if outNode := findMappingValue(valNode, "outputs"); outNode != nil {
				parsedOut, err := parseHarnessOutputs(outNode)
				if err != nil {
					diags = append(diags, Diagnostic{
						File:     file,
						Line:     outNode.Line,
						Column:   outNode.Column,
						Field:    "nodes." + nodeID + ".outputs",
						Code:     "invalid_outputs",
						Message:  err.Error(),
						Severity: "error",
					})
				} else {
					ns.Outputs = parsedOut
				}
			}
			def.Nodes[nodeID] = ns
		}
	}

	if len(diags) > 0 {
		return def, diags
	}
	return def, nil
}

func parseHarnessOutputs(node *yaml.Node) (*HarnessOutputsSpec, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("outputs must be a mapping")
	}
	spec := &HarnessOutputsSpec{Fields: make(map[string]OutputFieldSpec)}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		v := node.Content[i+1]
		if k == "$ref" {
			if v.Kind != yaml.ScalarNode {
				return nil, errors.New("$ref must be a string path")
			}
			spec.Ref = v.Value
			continue
		}
		var field OutputFieldSpec
		if err := v.Decode(&field); err != nil {
			return nil, fmt.Errorf("output field %q: %w", k, err)
		}
		spec.Fields[k] = field
	}
	if spec.Ref != "" && len(spec.Fields) > 0 {
		spec.Mixed = true
	}
	return spec, nil
}

func findMappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func collectLocations(file, prefix string, node *yaml.Node, locs map[string]Location) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		path := k.Value
		if prefix != "" {
			path = prefix + "." + k.Value
		}
		locs[path] = Location{File: file, Line: k.Line, Column: k.Column}
		if v.Kind == yaml.MappingNode {
			collectLocations(file, path, v, locs)
		}
	}
}

func (d *Definition) locFor(field string) Location {
	if loc, ok := d.Locations[field]; ok {
		return loc
	}
	parts := strings.Split(field, ".")
	for len(parts) > 1 {
		parts = parts[:len(parts)-1]
		if loc, ok := d.Locations[strings.Join(parts, ".")]; ok {
			return loc
		}
	}
	return Location{File: d.File, Line: 1, Column: 1}
}
