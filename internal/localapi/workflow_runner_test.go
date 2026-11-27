package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/engine"
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
	notifications, err := builtin.NewNotificationCenter(home.State)
	if err != nil {
		t.Fatal(err)
	}
	watches, err := engine.OpenWatchStore(filepath.Join(home.State, "watches"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &workflowRunner{ctx: ctx, state: state, library: worker, notifications: notifications, watches: watches, cancellations: make(map[string]context.CancelFunc)}
	t.Cleanup(func() { cancel(); runner.wg.Wait() })
	return runner, state, dir
}

func startFixtureWorkflow(t *testing.T, runner *workflowRunner, id string, inputs map[string]any) store.WorkflowRun {
	t.Helper()
	runner.library.reconcile(runner.ctx)
	entries := runner.library.snapshot().Entries
	var digest string
	for _, entry := range entries {
		if entry.ID == "main-flow" && entry.Status == "ready" {
			digest = entry.Digest
		}
	}
	if digest == "" {
		t.Fatalf("workflow was not prepared: %+v", entries)
	}
	req := workflowLaunchRequest{RequestID: id, WorkflowID: "main-flow", ExpectedDigest: digest, Inputs: inputs}
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

func TestWorkflowApprovalResumesOrStops(t *testing.T) {
	runner, state, _ := workflowRunnerFixture(t, `schema: flows/v1
id: main-flow
version: 1.0.0
inputs:
  timeout: {type: string, default: 3s}
nodes:
  approve:
    uses: core.notify
    with:
      approval: true
      title: Approve the change
      timeout: ${{ inputs.timeout }}
  next:
    uses: core.command
    needs: [approve]
    with: {command: /usr/bin/true}
outputs:
  approved: ${{ nodes.approve.outputs.approved }}
`)
	handler := QuestionsHandler(runner.notifications, runner.questionActive)
	for _, tc := range []struct {
		id, answer string
		expected   store.Status
	}{
		{"yes", "approve", store.Succeeded},
		{"no", "reject", store.Failed},
	} {
		run := startFixtureWorkflow(t, runner, tc.id, nil)
		var question string
		waitWorkflowFixture(t, runner.ctx, func() bool {
			view, _ := state.Snapshot(runner.ctx)
			for _, node := range view.Nodes {
				if node.RunID == run.RunID && node.Status == store.WaitingForInput {
					question = node.Wait.Ref
					return true
				}
			}
			return false
		})
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/v1/questions/"+question+"/answer", strings.NewReader(`{"answer":"incorrect"}`)))
		if rec.Code != 422 {
			t.Fatalf("invalid answer status %d", rec.Code)
		}
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/v1/questions/"+question+"/answer", strings.NewReader(`{"answer":"`+tc.answer+`"}`)))
		if rec.Code != 200 {
			t.Fatalf("answer was rejected: %s", rec.Body)
		}
		waitWorkflowFixture(t, runner.ctx, func() bool {
			record, _ := state.WorkflowRunByRequest(runner.ctx, tc.id)
			return record.Status == tc.expected
		})
	}
	startFixtureWorkflow(t, runner, "timeout", map[string]any{"timeout": "30ms"})
	waitWorkflowFixture(t, runner.ctx, func() bool {
		record, _ := state.WorkflowRunByRequest(runner.ctx, "timeout")
		return record.Status == store.Failed
	})
}

func TestWorkflowWatchPersistsCursorAndCancellation(t *testing.T) {
	runner, state, _ := workflowRunnerFixture(t, `schema: flows/v1
id: main-flow
version: 1.0.0
inputs:
  keep_watching: {type: boolean, default: false}
nodes:
  delay:
    uses: core.wait
    with: {duration: 10ms}
  poll:
    uses: fixture.poll
    needs: [delay]
    watch:
      interval: 40ms
      deadline: 3s
      until: ${{ outputs.done }}
    with:
      keep_watching: ${{ inputs.keep_watching }}
outputs:
  count: ${{ nodes.poll.outputs.count }}
`)
	tool := filepath.Join(runner.library.home.Tools(), "fixture")
	writePackageFile(t, tool, "tool.yaml", `schema: flows/tool/v1
id: fixture
version: 1.0.0
operations:
  - id: fixture.poll
    description: Poll a fixture
    execution_kind: plugin
    read_only: true
`)
	bin := filepath.Join(tool, "fixture-plugin")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
read -r line
case "$line" in
  *describe*) printf '%s\n' '{"protocol":1,"kind":"manifest","manifest":{"protocol":1,"id":"fixture","version":"1.0.0","operations":[{"id":"fixture.poll","description":"Poll a fixture","read_only":true}]}}' ;;
  *'"keep_watching":true'*) printf '%s\n' '{"protocol":1,"kind":"observation","observation":{"done":false,"cursor":"next","data":{"count":1}}}' ;;
  *'"cursor":"next"'*) printf '%s\n' '{"protocol":1,"kind":"observation","observation":{"done":true,"cursor":"last","data":{"count":2}}}' ;;
  *) printf '%s\n' '{"protocol":1,"kind":"observation","observation":{"done":false,"cursor":"next","data":{"count":1}}}' ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	startFixtureWorkflow(t, runner, "watch", nil)
	waitWorkflowFixture(t, runner.ctx, func() bool {
		record, _ := state.WorkflowRunByRequest(runner.ctx, "watch")
		return record.Status == store.Succeeded && string(record.Outputs) == `{"count":2}`
	})
	run := startFixtureWorkflow(t, runner, "cancel", map[string]any{"keep_watching": true})
	var watchID string
	waitWorkflowFixture(t, runner.ctx, func() bool {
		view, _ := state.Snapshot(runner.ctx)
		for _, node := range view.Nodes {
			if node.RunID == run.RunID && node.Kind == "watch" && node.Status == store.Running {
				watchID = node.ID
				return true
			}
		}
		return false
	})
	if !runner.cancelNode(watchID) {
		t.Fatal("watch was not cancellable")
	}
	waitWorkflowFixture(t, runner.ctx, func() bool {
		record, _ := state.WorkflowRunByRequest(runner.ctx, "cancel")
		return record.Status == store.Cancelled
	})
	raw, err := os.ReadFile(filepath.Join(runner.library.home.State, "watches", watchID+".json"))
	var stored engine.WatchState
	if err != nil || json.Unmarshal(raw, &stored) != nil || !stored.Cancelled {
		t.Fatalf("watch cancellation was not persisted: %s (%v)", raw, err)
	}
}
