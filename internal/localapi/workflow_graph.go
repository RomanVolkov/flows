package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

func workflowPredicate(expression string, bind workflow.BindContext) (bool, error) {
	var value any
	var err error
	if strings.Contains(expression, "${{") {
		value, err = workflow.ResolveValue(expression, bind)
	} else {
		value, err = workflow.EvalExpr(expression, bind)
	}
	if err != nil {
		return false, err
	}
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition %q must resolve to a boolean", expression)
	}
	return b, nil
}

type stepResult struct {
	id     string
	output map[string]any
	err    error
}

func (r *workflowRunner) executeGraph(ctx context.Context, run store.WorkflowRun, spec store.NewWorkflowRun,
	snapshot *workflow.LaunchSnapshot, steps []store.WorkflowStep, edges []store.WorkflowEdge, inputs map[string]any) (map[string]any, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	def := snapshot.Compiled.Definition
	bind := workflow.BindContext{Inputs: inputs, NodeOutputs: make(map[string]map[string]any), FanOutOutputs: make(map[string][]map[string]any)}
	dependencies := make(map[string][]store.WorkflowEdge)
	ids := make(map[string]string, len(steps))
	for _, step := range steps {
		ids[step.StepID] = step.NodeID
	}
	for _, edge := range edges {
		dependencies[edge.To] = append(dependencies[edge.To], edge)
	}
	finished := make(map[string]bool)
	active := make(map[string]bool)
	results := make(chan stepResult, len(steps))
	milestones := engine.NewMilestoneRegistry()
	var failure error
	for len(finished) < len(steps) {
		changes := r.state.Changes()
		previouslyFinished := len(finished)
		for _, step := range steps {
			if failure != nil || ctx.Err() != nil || len(active) >= 4 {
				break
			}
			if finished[step.StepID] || active[step.StepID] {
				continue
			}
			ready := true
			for _, edge := range dependencies[step.StepID] {
				allowed := finished[edge.From]
				if edge.Milestone != "" {
					allowed = milestones.IsReached(ids[edge.From], edge.Milestone)
					if !allowed {
						allowed, failure = r.state.WorkflowMilestoneReached(ctx, ids[edge.From], edge.Milestone)
						if failure != nil {
							break
						}
						if allowed {
							milestones.Publish(ids[edge.From], edge.Milestone, time.Now())
						}
					}
				}
				ready = ready && allowed
			}
			if failure != nil {
				break
			}
			if !ready {
				continue
			}
			node := def.Nodes[step.StepID]
			if node.When != "" {
				allowed, err := workflowPredicate(node.When, bind)
				if err != nil {
					failure = fmt.Errorf("step %s: %w", step.StepID, err)
					break
				}
				if !allowed {
					if err := r.state.FinishWorkflowNode(ctx, step.NodeID, store.Skipped, nil, ""); err != nil {
						failure = err
						break
					}
					finished[step.StepID] = true
					bind.NodeOutputs[step.StepID] = map[string]any{}
					if node.ForEach != nil {
						bind.FanOutOutputs[step.StepID] = []map[string]any{}
					}
					continue
				}
			}
			local := bind
			local.NodeOutputs = maps.Clone(bind.NodeOutputs)
			local.FanOutOutputs = maps.Clone(bind.FanOutOutputs)
			active[step.StepID] = true
			go func() {
				out, err := r.executeWorkflowStep(ctx, run, spec, step, node, snapshot, local, len(dependencies[step.StepID]) > 0)
				results <- stepResult{step.StepID, out, err}
			}()
		}
		if ctx.Err() != nil && failure == nil {
			failure = ctx.Err()
		}
		if failure != nil {
			stop()
		}
		if len(active) == 0 {
			if failure == nil && previouslyFinished != len(finished) && len(finished) < len(steps) {
				continue
			}
			if failure == nil && len(finished) < len(steps) {
				failure = errors.New("workflow has dependencies that never reached their required milestone")
			}
			break
		}
		if failure != nil {
			result := <-results
			delete(active, result.id)
			finished[result.id] = true
			continue
		}
		select {
		case result := <-results:
			delete(active, result.id)
			finished[result.id] = true
			if result.err != nil {
				failure = fmt.Errorf("step %s: %w", result.id, result.err)
				continue
			}
			bind.NodeOutputs[result.id] = result.output
			if rows, ok := result.output["items"].([]map[string]any); ok && def.Nodes[result.id].ForEach != nil {
				bind.FanOutOutputs[result.id] = rows
			}
		case <-changes:
		case <-ctx.Done():
		}
	}
	if failure != nil {
		if r.ctx.Err() == nil {
			for _, step := range steps {
				if node, err := r.state.Node(r.ctx, step.NodeID); err == nil && (node.Status == store.Ready || node.Status == store.WaitingForDependencies) {
					_ = r.state.FinishWorkflowNode(r.ctx, step.NodeID, store.Cancelled, nil, "")
				}
			}
		}
		return nil, failure
	}
	outputs := make(map[string]any)
	for name, expression := range def.Outputs {
		value, err := workflow.ResolveValue(expression, bind)
		if err != nil {
			return nil, err
		}
		outputs[name] = value
	}
	return outputs, nil
}

func (r *workflowRunner) registerWorkflowChildren(run store.WorkflowRun, steps []store.WorkflowStep) func() {
	r.mu.Lock()
	cancel := r.cancellations[run.NodeID]
	for _, step := range steps {
		if cancel != nil {
			r.cancellations[step.NodeID] = cancel
		}
	}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		for _, step := range steps {
			delete(r.cancellations, step.NodeID)
		}
		r.mu.Unlock()
	}
}

func (r *workflowRunner) expandWorkflowStep(ctx context.Context, run store.WorkflowRun, spec store.NewWorkflowRun,
	step store.WorkflowStep, node workflow.NodeSpec, snapshot *workflow.LaunchSnapshot, bind workflow.BindContext) (map[string]any, error) {
	if node.ForEach != nil {
		raw, err := resolveWorkflowExpression(node.ForEach.Items, bind)
		if err != nil {
			return nil, err
		}
		var items []any
		switch values := raw.(type) {
		case []any:
			items = values
		case []map[string]any:
			for _, value := range values {
				items = append(items, value)
			}
		default:
			return nil, fmt.Errorf("for_each items must be an array")
		}
		if len(items) > 1000 {
			return nil, fmt.Errorf("for_each exceeds the 1000 item limit")
		}
		children, err := engine.ExpandFanOut(run.RunID, step.NodeID, step.StepID, items, func(item any) (string, error) {
			local := bind
			local.Item = item
			value, err := resolveWorkflowExpression(node.ForEach.Key, local)
			if err != nil {
				return "", err
			}
			switch key := value.(type) {
			case string:
				return key, nil
			case int, float64, bool:
				return fmt.Sprint(key), nil
			default:
				return "", fmt.Errorf("for_each key must be a string, number or boolean")
			}
		})
		if err != nil {
			return nil, err
		}
		node.ForEach, node.When = nil, ""
		steps := make([]store.WorkflowStep, 0, len(children))
		for _, child := range children {
			next := operationStep(step.StepID, step.Key+"/item/"+url.PathEscape(child.ItemKey), node, snapshot)
			if node.Repeat != nil {
				next.Kind = "group"
			}
			next.Label = step.StepID + " [" + child.ItemKey + "]"
			next.Item, err = json.Marshal(items[child.ItemIndex])
			if err != nil {
				return nil, err
			}
			steps = append(steps, next)
		}
		if err := r.state.AddWorkflowSteps(ctx, run.RunID, step.NodeID, steps, nil); err != nil {
			return nil, err
		}
		defer r.registerWorkflowChildren(run, steps)()
		for i, child := range steps {
			local := bind
			local.Item = items[i]
			children[i].Outputs, err = r.executeWorkflowStep(ctx, run, spec, child, node, snapshot, local, false)
			if err != nil {
				for _, pending := range steps[i+1:] {
					if r.ctx.Err() == nil {
						_ = r.state.FinishWorkflowNode(r.ctx, pending.NodeID, store.Cancelled, nil, "")
					}
				}
				return nil, err
			}
		}
		return map[string]any{"items": engine.JoinFanOut(children)}, nil
	}
	if node.Repeat != nil {
		rule := node.Repeat
		node.Repeat, node.When = nil, ""
		for iteration := 1; ; iteration++ {
			child := operationStep(step.StepID, fmt.Sprintf("%s/repeat/%d", step.Key, iteration), node, snapshot)
			child.Iteration = iteration
			child.Label = fmt.Sprintf("%s [%d]", step.StepID, iteration)
			if err := r.state.AddWorkflowSteps(ctx, run.RunID, step.NodeID, []store.WorkflowStep{child}, nil); err != nil {
				return nil, err
			}
			unregister := r.registerWorkflowChildren(run, []store.WorkflowStep{child})
			out, err := r.executeWorkflowStep(ctx, run, spec, child, node, snapshot, bind, false)
			unregister()
			if err != nil {
				return nil, err
			}
			local := bind
			local.CurrentOutputs = out
			local.NodeOutputs = maps.Clone(bind.NodeOutputs)
			local.NodeOutputs[step.StepID] = out
			satisfied := false
			if rule.Until != "" {
				satisfied, err = workflowPredicate(rule.Until, local)
				if err != nil {
					return nil, err
				}
			}
			_, done, exhausted := engine.NextRepeatOccurrence(run.RunID, step.NodeID, step.StepID, iteration, rule.Max, satisfied || rule.Until == "" && iteration >= rule.Max)
			if done {
				return out, nil
			}
			if exhausted {
				return out, fmt.Errorf("repeat reached its limit of %d iterations", rule.Max)
			}
		}
	}
	child := snapshot.ChildSnapshots[node.Workflow]
	if child == nil {
		return nil, fmt.Errorf("nested workflow %q was not pinned", node.Workflow)
	}
	raw, err := workflow.ResolveValue(node.With, bind)
	if err != nil {
		return nil, err
	}
	with, _ := raw.(map[string]any)
	inputs, err := workflow.ResolveInputs(child.Compiled.Definition, with)
	if err != nil {
		return nil, err
	}
	children, edges := graphSteps(child, step.Key), graphEdges(child)
	if err := r.state.AddWorkflowSteps(ctx, run.RunID, step.NodeID, children, edges); err != nil {
		return nil, err
	}
	defer r.registerWorkflowChildren(run, children)()
	return r.executeGraph(ctx, run, spec, child, children, edges, inputs)
}

func resolveWorkflowExpression(expr string, bind workflow.BindContext) (any, error) {
	if strings.Contains(expr, "${{") {
		return workflow.ResolveValue(expr, bind)
	}
	return workflow.EvalExpr(expr, bind)
}
