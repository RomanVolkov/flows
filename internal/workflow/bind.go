package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// BindContext holds resolved runtime values for expression evaluation.
type BindContext struct {
	Inputs      map[string]any
	NodeOutputs map[string]map[string]any
	Artifacts   map[string]string
	Item        any
	Secrets     []string
}

// ResolveInputs validates required inputs, applies defaults, and checks types.
func ResolveInputs(def *Definition, raw map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(def.Inputs))
	for name, spec := range def.Inputs {
		val, provided := raw[name]
		if !provided || val == nil {
			if spec.Default != nil {
				val = spec.Default
			} else if spec.Required {
				return nil, fmt.Errorf("missing required input %q", name)
			} else {
				continue
			}
		}
		if spec.Type != "" && !matchesType(val, spec.Type) {
			return nil, fmt.Errorf("input %q value %v does not match type %q", name, val, spec.Type)
		}
		if len(spec.Enum) > 0 && !enumContains(spec.Enum, val) {
			return nil, fmt.Errorf("input %q must be one of the allowed choices", name)
		}
		resolved[name] = val
	}
	return resolved, nil
}

// Compare the JSON representations so YAML integers and equivalent JSON numbers match.
func enumContains(choices []any, value any) bool {
	actual, err := json.Marshal(value)
	if err != nil {
		return false
	}
	for _, choice := range choices {
		expected, err := json.Marshal(choice)
		if err == nil && bytes.Equal(expected, actual) {
			return true
		}
	}
	return false
}

// ResolveValue recursively evaluates ${{ ... }} expressions in v.
func ResolveValue(v any, ctx BindContext) (any, error) {
	switch x := v.(type) {
	case string:
		trimmed := strings.TrimSpace(x)
		if strings.HasPrefix(trimmed, "${{") && strings.HasSuffix(trimmed, "}}") && strings.Count(trimmed, "${{") == 1 {
			expr := strings.TrimSpace(trimmed[3 : len(trimmed)-2])
			return EvalExpr(expr, ctx)
		}
		var out strings.Builder
		rest := x
		for {
			start := strings.Index(rest, "${{")
			if start < 0 {
				out.WriteString(rest)
				break
			}
			out.WriteString(rest[:start])
			end := strings.Index(rest[start+3:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("unterminated expression in %q", x)
			}
			expr := strings.TrimSpace(rest[start+3 : start+3+end])
			val, err := EvalExpr(expr, ctx)
			if err != nil {
				return nil, err
			}
			out.WriteString(fmt.Sprint(val))
			rest = rest[start+3+end+2:]
		}
		return out.String(), nil
	case map[string]any:
		res := make(map[string]any, len(x))
		for k, val := range x {
			rv, err := ResolveValue(val, ctx)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", k, err)
			}
			res[k] = rv
		}
		return res, nil
	case []any:
		res := make([]any, len(x))
		for i, val := range x {
			rv, err := ResolveValue(val, ctx)
			if err != nil {
				return nil, err
			}
			res[i] = rv
		}
		return res, nil
	default:
		return v, nil
	}
}

// EvalExpr resolves a dot-separated binding path against ctx.
func EvalExpr(expr string, ctx BindContext) (any, error) {
	parts := strings.Split(strings.TrimSpace(expr), ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("empty expression")
	}
	switch parts[0] {
	case "inputs":
		if len(parts) < 2 {
			return nil, fmt.Errorf("incomplete inputs binding %q", expr)
		}
		val, ok := ctx.Inputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("unbound input %q", parts[1])
		}
		return traversePath(val, parts[2:], expr)
	case "item":
		if ctx.Item == nil {
			return nil, fmt.Errorf("item binding %q used without active item", expr)
		}
		return traversePath(ctx.Item, parts[1:], expr)
	case "artifacts":
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid artifact binding %q", expr)
		}
		val, ok := ctx.Artifacts[parts[1]]
		if !ok {
			return nil, fmt.Errorf("missing artifact %q", parts[1])
		}
		return val, nil
	case "nodes":
		if len(parts) < 3 || parts[2] != "outputs" {
			return nil, fmt.Errorf("invalid node output binding %q", expr)
		}
		nodeOut, ok := ctx.NodeOutputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("missing outputs for node %q", parts[1])
		}
		if len(parts) == 3 {
			return nodeOut, nil
		}
		return traversePath(nodeOut, parts[3:], expr)
	default:
		return nil, fmt.Errorf("unsupported binding root %q", parts[0])
	}
}

func traversePath(cur any, rest []string, fullExpr string) (any, error) {
	for _, seg := range rest {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot traverse %q in %q (not an object)", seg, fullExpr)
		}
		next, exists := m[seg]
		if !exists {
			return nil, fmt.Errorf("missing nested field %q in %q", seg, fullExpr)
		}
		cur = next
	}
	return cur, nil
}

// Redact replaces any non-empty secret strings in text with [REDACTED].
func Redact(text string, secrets []string) string {
	out := text
	for _, s := range secrets {
		if s != "" {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	return out
}
