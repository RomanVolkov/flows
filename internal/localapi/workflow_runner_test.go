package localapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/store"
)

func workflowRunnerFixture(t *testing.T, definition string) (*workflowRunner, *store.Store, string) {
	t.Helper()
	home := libraryTestHome(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	dir := filepath.Join(home.Workflows(), "main-flow")
	writePackageFile(t, dir, "workflow.yaml", definition)
	state, err := store.Open(ctx, filepath.Join(home.State, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	worker := newLibraryReconciler(home)
	runner := &workflowRunner{ctx: ctx, state: state, library: worker, cancellations: make(map[string]context.CancelFunc)}
	t.Cleanup(func() { cancel(); runner.wg.Wait() })
	return runner, state, dir
}

func startFixtureWorkflow(t *testing.T, runner *workflowRunner, id string, inputs map[string]any) store.WorkflowRun {
	t.Helper()
	runner.library.reconcile(runner.ctx)
	entries := runner.library.snapshot().Entries
	if len(entries) != 1 || entries[0].Status != "ready" {
		t.Fatalf("workflow was not prepared: %+v", entries)
	}
	req := workflowLaunchRequest{RequestID: id, WorkflowID: entries[0].ID, ExpectedDigest: entries[0].Digest, Inputs: inputs}
	spec, snap, err := runner.prepareLaunch(req)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := runner.state.CreateWorkflowRun(runner.ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	runner.start(run, spec, snap, req)
	return run
}

func waitWorkflowFixture(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("workflow condition timed out")
		}
	}
}

func TestWorkflowGraphExpandsSubflowsAndRepeats(t *testing.T) {
	runner, state, dir := workflowRunnerFixture(t, `schema: flows/v1
id: main-flow
version: 1.0.0
inputs:
  enabled: {type: boolean, default: false}
  items: {type: array, required: true}
nodes:
  finish:
    uses: core.command
    needs: [optional, groups, repeat]
    with:
      command: /usr/bin/true
  optional:
    uses: core.command
    when: ${{ inputs.enabled }}
    with: {command: /usr/bin/false}
  groups:
    workflow: nested/child.yaml
    for_each:
      items: ${{ inputs.items }}
      key: ${{ item }}
    with:
      item: ${{ item }}
  repeat:
    uses: core.command
    repeat: {max: 2}
    with: {command: /usr/bin/true}
outputs:
  groups: ${{ nodes.groups.outputs }}
`)
	writePackageFile(t, filepath.Join(dir, "nested"), "child.yaml", `schema: flows/v1
id: nested
version: 1.0.0
inputs:
  item: {type: string, required: true}
nodes:
  echo:
    workflow: deeper.yaml
    with:
      text: ${{ inputs.item }}
outputs:
  text: ${{ nodes.echo.outputs.text }}
`)
	writePackageFile(t, filepath.Join(dir, "nested"), "deeper.yaml", `schema: flows/v1
id: leaf
version: 1.0.0
inputs:
  text: {type: string, required: true}
nodes:
  echo:
    uses: core.command
    with:
      command: printf
      args: ['%s', '${{ inputs.text }}']
outputs:
  text: ${{ nodes.echo.outputs.stdout }}
`)
	startFixtureWorkflow(t, runner, "ok", map[string]any{"items": []any{"first", "second"}})
	var result store.WorkflowRun
	waitWorkflowFixture(t, runner.ctx, func() bool {
		result, _ = state.WorkflowRunByRequest(runner.ctx, "ok")
		return result.Status == store.Succeeded || result.Status == store.Failed
	})
	if result.Status != store.Succeeded || !strings.Contains(string(result.Outputs), `"groups":[{"text":"first\n"},{"text":"second\n"}]`) {
		t.Fatalf("unexpected graph result: %+v", result)
	}
	view, err := state.Snapshot(runner.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var skipped, repeats int
	for _, node := range view.Nodes {
		if node.Name == "optional" && node.Status == store.Skipped {
			skipped++
		}
		if strings.HasPrefix(node.Name, "repeat [") && node.Status == store.Succeeded {
			repeats++
		}
	}
	if skipped != 1 || repeats != 2 {
		t.Fatalf("missing graph states: skipped=%d repeated=%d", skipped, repeats)
	}
	startFixtureWorkflow(t, runner, "duplicate", map[string]any{"items": []any{"same", "same"}})
	waitWorkflowFixture(t, runner.ctx, func() bool {
		result, _ = state.WorkflowRunByRequest(runner.ctx, "duplicate")
		return result.Status == store.Failed
	})
}

func TestWorkflowMilestoneUnblocksBeforeCompletion(t *testing.T) {
	runner, state, _ := workflowRunnerFixture(t, `schema: flows/v1
id: main-flow
version: 1.0.0
milestones: [ready]
inputs:
  gate: {type: string, required: true}
nodes:
  producer:
    uses: core.command
    with:
      command: /bin/sh
      args: [-c, 'while [ ! -f "$1" ]; do sleep 0.05; done', gate, '${{ inputs.gate }}']
  consumer:
    uses: core.command
    needs: [producer.ready]
    with: {command: /usr/bin/true}
`)
	gate := filepath.Join(t.TempDir(), "gate")
	run := startFixtureWorkflow(t, runner, "early", map[string]any{"gate": gate})
	var producer store.Node
	waitWorkflowFixture(t, runner.ctx, func() bool {
		view, _ := state.Snapshot(runner.ctx)
		for _, node := range view.Nodes {
			if node.Name == "producer" && node.Status == store.Running {
				producer = node
				return true
			}
		}
		return false
	})
	if err := state.RecordWorkflowMilestone(runner.ctx, producer.ID, *producer.AttemptID, "ready"); err != nil {
		t.Fatal(err)
	}
	waitWorkflowFixture(t, runner.ctx, func() bool {
		view, _ := state.Snapshot(runner.ctx)
		for _, node := range view.Nodes {
			if node.Name == "consumer" && node.Status == store.Succeeded {
				return true
			}
		}
		return false
	})
	current, err := state.Node(runner.ctx, producer.ID)
	if err != nil || current.Status != store.Running {
		t.Fatalf("producer completed before milestone consumer: %+v %v", current, err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitWorkflowFixture(t, runner.ctx, func() bool {
		node, _ := state.Node(runner.ctx, run.NodeID)
		return node.Status == store.Succeeded
	})
}

func TestWorkflowRepeatUntilAndLimit(t *testing.T) {
	runner, state, _ := workflowRunnerFixture(t, `schema: flows/v1
id: main-flow
version: 1.0.0
inputs:
  done: {type: boolean, required: true}
nodes:
  repeat:
    uses: core.command
    repeat:
      max: 2
      until: ${{ inputs.done }}
    with: {command: /usr/bin/true}
`)
	for _, tc := range []struct {
		id          string
		done        bool
		want        store.Status
		occurrences int
	}{{"satisfied", true, store.Succeeded, 1}, {"exhausted", false, store.Failed, 2}} {
		run := startFixtureWorkflow(t, runner, tc.id, map[string]any{"done": tc.done})
		waitWorkflowFixture(t, runner.ctx, func() bool {
			result, _ := state.WorkflowRunByRequest(runner.ctx, tc.id)
			return result.Status == tc.want
		})
		view, err := state.Snapshot(runner.ctx)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, node := range view.Nodes {
			if node.RunID == run.RunID && strings.HasPrefix(node.Name, "repeat [") {
				count++
			}
		}
		if count != tc.occurrences {
			t.Fatalf("%s executed %d occurrences", tc.id, count)
		}
	}
}
