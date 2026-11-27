package localapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/plugin"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

var errStaleWorkflow = errors.New("workflow is unavailable or has changed since it was selected")

type workflowRunner struct {
	ctx           context.Context
	state         *store.Store
	runs          *RunManager
	results       map[string]*harnessResultScope
	library       *libraryReconciler
	mu            sync.Mutex
	cancellations map[string]context.CancelFunc
	wg            sync.WaitGroup
}

func (r *workflowRunner) prepareLaunch(req workflowLaunchRequest) (store.NewWorkflowRun, *workflow.LaunchSnapshot, error) {
	w := r.library
	w.mu.RLock()
	reg, tools := w.registry, w.tools
	index := w.index
	w.mu.RUnlock()
	entries := make(map[string]library.IndexEntry)
	for _, entry := range index.Entries {
		if entry.Kind == library.KindWorkflow && entry.Status == "ready" {
			entries[entry.ID] = entry
		}
	}
	entry, found := entries[req.WorkflowID]
	if !found || reg == nil || entry.Digest != req.ExpectedDigest || !currentPackage(w, entry) {
		return store.NewWorkflowRun{}, nil, errStaleWorkflow
	}
	loader := libraryWorkflowLoader(entries)
	def, diags := workflow.ParseFile(filepath.Join(entry.Path, "workflow.yaml"))
	if len(diags) > 0 {
		return store.NewWorkflowRun{}, nil, diags[0]
	}
	inputs, err := workflow.ResolveInputs(def, req.Inputs)
	if err != nil {
		return store.NewWorkflowRun{}, nil, err
	}
	for name := range req.Inputs {
		if _, known := def.Inputs[name]; !known {
			return store.NewWorkflowRun{}, nil, fmt.Errorf("unknown input %q", name)
		}
	}
	cw, diags := workflow.CompileWithLoader(def, reg, loader)
	if len(diags) > 0 {
		return store.NewWorkflowRun{}, nil, diags[0]
	}
	snapshot, err := workflow.CreateSnapshotWithLoader(entry.Path, cw, reg, loader)
	if err != nil {
		return store.NewWorkflowRun{}, nil, err
	}
	if err := pinWorkflowTools(snapshot, reg, tools, w); err != nil {
		return store.NewWorkflowRun{}, nil, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return store.NewWorkflowRun{}, nil, err
	}
	inputData, err := json.Marshal(inputs)
	if err != nil {
		return store.NewWorkflowRun{}, nil, err
	}
	dir := req.WorkDir
	if dir == "" {
		settings, err := config.LoadSettings(w.home.Settings())
		if err != nil {
			return store.NewWorkflowRun{}, nil, err
		}
		dir = settings.DefaultDirectory
	}
	if dir != "" {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() || !filepath.IsAbs(dir) {
			return store.NewWorkflowRun{}, nil, fmt.Errorf("working directory must be an existing absolute directory")
		}
	}
	spec := store.NewWorkflowRun{RequestID: req.RequestID, WorkflowID: def.ID, Digest: entry.Digest, SnapshotDigest: fmt.Sprintf("%x", sha256.Sum256(data)),
		PackageDir: entry.Path, WorkDir: dir, Inputs: inputData, Snapshot: data,
		Steps: graphSteps(snapshot, ""), Edges: graphEdges(snapshot)}
	if !currentPackage(w, entry) {
		return store.NewWorkflowRun{}, nil, errStaleWorkflow
	}
	return spec, snapshot, nil
}

func pinWorkflowTools(snapshot *workflow.LaunchSnapshot, reg *catalog.Registry, tools map[string]preparedTool, worker *libraryReconciler) error {
	for operation := range snapshot.OperationVersions {
		op, pkg, err := reg.Describe(operation)
		if err != nil {
			return err
		}
		if op.ExecutionKind != catalog.ExecPlugin {
			continue
		}
		tool, found := tools[pkg.PackageID]
		if !found || tool.err != nil || !currentPackage(worker, tool.entry) {
			return fmt.Errorf("%w: tool %s changed", errStaleWorkflow, pkg.PackageID)
		}
		if snapshot.ToolBuilds == nil {
			snapshot.ToolBuilds = make(map[string]workflow.ToolBuildPin)
		}
		snapshot.ToolBuilds[operation] = workflow.ToolBuildPin{ToolID: pkg.PackageID, CacheKey: tool.active.Receipt.CacheKey, BinaryDigest: tool.active.Receipt.BinaryDigest}
	}
	for _, child := range snapshot.ChildSnapshots {
		if err := pinWorkflowTools(child, reg, tools, worker); err != nil {
			return err
		}
	}
	return nil
}

func graphSteps(snapshot *workflow.LaunchSnapshot, parentKey string) []store.WorkflowStep {
	def := snapshot.Compiled.Definition
	steps := make([]store.WorkflowStep, 0, len(def.NodeOrder))
	for _, id := range def.NodeOrder {
		node := def.Nodes[id]
		key := id
		if parentKey != "" {
			key = parentKey + "/" + id
		}
		step := operationStep(id, key, node, snapshot)
		if node.ForEach != nil || node.Repeat != nil || node.Workflow != "" {
			step.Kind = "group"
		}
		steps = append(steps, step)
	}
	return steps
}

func operationStep(id, key string, node workflow.NodeSpec, snapshot *workflow.LaunchSnapshot) store.WorkflowStep {
	pin := snapshot.ToolBuilds[node.Uses]
	step := store.WorkflowStep{NodeID: rand.Text(), StepID: id, Key: key, Kind: "tool", InvocationID: rand.Text(),
		ToolID: pin.ToolID, CacheKey: pin.CacheKey, BinaryDigest: pin.BinaryDigest}
	switch {
	case node.Workflow != "":
		step.Kind = "group"
	case node.Uses == "core.harness":
		step.Kind = "session"
	case node.Watch != nil:
		step.Kind = "watch"
	}
	return step
}

func graphEdges(snapshot *workflow.LaunchSnapshot) []store.WorkflowEdge {
	seen := make(map[store.WorkflowEdge]bool)
	var edges []store.WorkflowEdge
	for _, edge := range snapshot.Compiled.Edges {
		dependency := store.WorkflowEdge{From: edge.FromID, To: edge.ToID, Milestone: edge.Milestone}
		if !seen[dependency] {
			edges = append(edges, dependency)
			seen[dependency] = true
		}
	}
	return edges
}

func currentPackage(w *libraryReconciler, entry library.IndexEntry) bool {
	current, err := library.Preview(w.home, entry.Path)
	return err == nil && current.Kind == entry.Kind && current.ID == entry.ID && current.Digest == entry.Digest
}

func (r *workflowRunner) start(run store.WorkflowRun, spec store.NewWorkflowRun, snapshot *workflow.LaunchSnapshot, req workflowLaunchRequest) {
	ctx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.wg.Add(1)
	for _, step := range spec.Steps {
		r.cancellations[step.NodeID] = cancel
	}
	r.cancellations[run.NodeID] = cancel
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.cancellations, run.NodeID)
			for _, step := range spec.Steps {
				delete(r.cancellations, step.NodeID)
			}
			r.mu.Unlock()
		}()
		r.execute(ctx, run, spec, snapshot, req)
	}()
}

func (r *workflowRunner) cancelNode(id string) bool {
	r.mu.Lock()
	cancel, found := r.cancellations[id]
	r.mu.Unlock()
	if found {
		cancel()
	}
	return found
}

func (r *workflowRunner) execute(ctx context.Context, run store.WorkflowRun, spec store.NewWorkflowRun, snapshot *workflow.LaunchSnapshot, req workflowLaunchRequest) {
	if _, err := r.state.StartAttempt(ctx, run.NodeID); err != nil {
		return
	}
	inputs, err := workflow.ResolveInputs(snapshot.Compiled.Definition, req.Inputs)
	var output map[string]any
	if err == nil {
		output, err = r.executeGraph(ctx, run, spec, snapshot, spec.Steps, spec.Edges, inputs)
	}
	status := store.Succeeded
	if r.ctx.Err() != nil {
		return
	}
	saveCtx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
	defer cancel()
	message := ""
	if err != nil {
		status = store.Failed
		message = err.Error()
	}
	if ctx.Err() != nil {
		status = store.Cancelled
	}

	_ = r.state.FinishWorkflowNode(saveCtx, run.NodeID, status, output, message)
}

func (r *workflowRunner) executeWorkflowStep(ctx context.Context, run store.WorkflowRun, spec store.NewWorkflowRun, step store.WorkflowStep, node workflow.NodeSpec, snapshot *workflow.LaunchSnapshot, bind workflow.BindContext, waiting bool) (map[string]any, error) {
	if waiting {
		if err := r.state.Transition(ctx, step.NodeID, store.Ready, nil); err != nil {
			return nil, err
		}
	}
	attempt, err := r.state.StartAttempt(ctx, step.NodeID)
	if err != nil {
		return nil, err
	}
	var raw any
	if node.ForEach == nil {
		raw, err = workflow.ResolveValue(node.With, bind)
	}
	with, _ := raw.(map[string]any)
	var outputs map[string]any
	if err == nil {
		if err = r.state.MarkWorkflowStepInFlight(ctx, step.NodeID); err == nil {
			if node.ForEach != nil || node.Repeat != nil || node.Workflow != "" {
				outputs, err = r.expandWorkflowStep(ctx, run, spec, step, node, snapshot, bind)
			} else {
				outputs, err = r.invokeWorkflowStep(ctx, run, spec, step, node, attempt, snapshot, with)
			}
		}
	}
	if r.ctx.Err() != nil {
		return nil, ctx.Err()
	}
	status := store.Succeeded
	message := ""
	if err != nil {
		status, message = store.Failed, err.Error()
	}
	if ctx.Err() != nil {
		status = store.Cancelled
	}
	if saveErr := r.state.FinishWorkflowNode(r.ctx, step.NodeID, status, outputs, message); saveErr != nil {
		return nil, saveErr
	}
	if node.Uses == "core.harness" && status == store.Succeeded {
		if outbox, err := host.NewOutbox(r.library.home.State, attempt.ID); err == nil {
			_ = outbox.MarkDelivered("workflow-result")
		}
	}
	return outputs, err
}

func (r *workflowRunner) invokeWorkflowStep(ctx context.Context, run store.WorkflowRun, spec store.NewWorkflowRun, step store.WorkflowStep, node workflow.NodeSpec, attempt store.Attempt, snapshot *workflow.LaunchSnapshot, with map[string]any) (map[string]any, error) {
	if node.Watch != nil {
		return nil, fmt.Errorf("workflow step %s uses an unsupported execution rule", step.StepID)
	}
	if node.Uses == "core.harness" {
		return r.executeHarness(ctx, spec, step, attempt, snapshot, with)
	}
	if node.Uses == "core.command" {
		command, _ := with["command"].(string)
		args := make([]string, 0)
		if value, set := with["args"]; set {
			items, ok := value.([]any)
			if !ok {
				return nil, errors.New("command args must be an array")
			}
			for _, item := range items {
				if item == nil {
					return nil, errors.New("command args must not contain null")
				}
				args = append(args, fmt.Sprint(item))
			}
		}
		dir := spec.WorkDir
		if dir == "" {
			dir, _ = with["working_dir"].(string)
		}
		result, err := builtin.RunCommand(ctx, builtin.CommandSpec{Executable: command, Args: args, WorkDir: dir})
		out := map[string]any{"exit_code": result.ExitCode, "stdout": result.Stdout, "stderr": result.Stderr}
		return out, err
	}
	if step.ToolID == "" || step.CacheKey == "" || step.BinaryDigest == "" {
		return nil, fmt.Errorf("operation %q is not executable", node.Uses)
	}
	home := r.library.home
	binary := filepath.Join(home.State, "builds", step.ToolID, step.CacheKey, "plugin")
	data, err := os.ReadFile(binary)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(fmt.Sprintf("%x", sha256.Sum256(data)), step.BinaryDigest) {
		return nil, fmt.Errorf("pinned tool %s failed binary integrity verification", step.ToolID)
	}
	result, err := plugin.NewRegistry(home).InvokePinned(ctx, step.ToolID, step.CacheKey, plugin.InvokeConfig{
		Operation: node.Uses, AttemptID: attempt.ID, RunID: run.RunID, RequestID: step.InvocationID, Input: with,
		OnMilestone: func(name string) error {
			if !slices.Contains(snapshot.Compiled.Definition.Milestones, name) {
				return fmt.Errorf("operation %q reported undeclared workflow milestone %q", node.Uses, name)
			}
			return r.state.RecordWorkflowMilestone(ctx, step.NodeID, attempt.ID, name)
		},
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]any)
	if len(result.Output) > 0 {
		err = json.Unmarshal(result.Output, &out)
	}
	return out, err
}
