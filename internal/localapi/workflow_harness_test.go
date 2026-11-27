package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestHarnessResultBridgeRejectsAndAcceptsScopedResults(t *testing.T) {
	schema, err := harnessResultSchema(workflow.PortSpec{Fields: map[string]workflow.OutputFieldSpec{
		"approved": {Type: "boolean", Required: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := host.NewOutbox(t.TempDir(), "attempt")
	if err != nil {
		t.Fatal(err)
	}
	scope := &harnessResultScope{token: "expected", schema: schema, outbox: outbox, result: make(chan json.RawMessage, 1)}
	runner := &workflowRunner{results: map[string]*harnessResultScope{"attempt": scope}}
	submit := func(body string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/workflows/result", strings.NewReader(body))
		res := httptest.NewRecorder()
		runner.submitHarnessResult(res, req)
		if res.Code != status {
			t.Fatalf("got status %d, want %d: %s", res.Code, status, res.Body)
		}
	}
	submit(`{"attempt_id":"attempt","token":"unrelated","output":{"approved":true}}`, 403)
	submit(`{"attempt_id":"attempt","token":"expected","output":{"approved":"true"}}`, 422)
	select {
	case <-scope.result:
		t.Fatal("invalid response reached downstream")
	default:
	}
	submit(`{"attempt_id":"attempt","token":"expected","output":{"approved":true}}`, 200)
	submit(`{"attempt_id":"attempt","token":"expected","output":{"approved":false}}`, 409)
	select {
	case result := <-scope.result:
		if string(result) != `{"approved":true}` {
			t.Fatalf("wrong output %s", result)
		}
	default:
		t.Fatal("valid result did not reach downstream")
	}
	pending, err := outbox.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("result was not persisted: %+v %v", pending, err)
	}
}

func TestWorkflowHarnessUsesPinnedPromptAndStructuredOutput(t *testing.T) {
	home := libraryTestHome(t)
	workdir := t.TempDir()
	script := filepath.Join(workdir, "codex-fixture")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
case "$1" in
--version|--help) echo 'codex fixture'; exit 0 ;;
esac
printf '%s\n' "$@" > prompt.txt
case "$*" in
*"fail case"*) exit 7;;
esac
printf '%s\n%s\n' "$FLOWS_ATTEMPT_ID" "$FLOWS_ATTEMPT_TOKEN" > ready.tmp
mv ready.tmp ready.txt
while :; do sleep 0.05; done
`), 0700); err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadSettings(home.Settings())
	if err != nil {
		t.Fatal(err)
	}
	settings.Harnesses = map[harness.Kind]config.HarnessSettings{harness.KindCodex: {Executable: script}}
	if _, err := config.SaveSettings(home.Settings(), settings.Revision, settings); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home.Workflows(), "harness-test")
	writePackageFile(t, dir, "workflow.yaml", `schema: flows/v1
id: harness-test
version: 1.0.0
inputs:
  name: {type: string, required: true}
nodes:
  agent:
    uses: core.harness
    with:
      harness: codex
      prompt_template: prompt.md
      arguments:
        name: ${{ inputs.name }}
    outputs:
      approved: {type: boolean, required: true}
  consume:
    uses: core.command
    with:
      command: printf
      args: ['%s', '${{ nodes.agent.outputs.approved }}']
outputs:
  approved: ${{ nodes.agent.outputs.approved }}
`)
	writePackageFile(t, dir, "prompt.md", "Review {{ name }}")
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(home.State, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	worker := newLibraryReconciler(home)
	worker.reconcile(ctx)
	sessions := NewSessionManager(st, home.State)
	runs := NewRunManager(home, st, sessions)
	defer runs.CloseHosts()
	runner := &workflowRunner{ctx: ctx, state: st, library: worker, runs: runs, cancellations: make(map[string]context.CancelFunc)}
	defer func() { cancel(); runner.wg.Wait() }()
	digest := worker.snapshot().Entries[0].Digest
	start := func(id, name string) {
		t.Helper()
		req := workflowLaunchRequest{RequestID: id, WorkflowID: "harness-test", ExpectedDigest: digest, WorkDir: workdir, Inputs: map[string]any{"name": name}}
		spec, snap, err := runner.prepareLaunch(req)
		if err != nil {
			t.Fatal(err)
		}
		run, _, err := st.CreateWorkflowRun(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		runner.start(run, spec, snap, req)
	}
	wait := func(check func() bool) {
		t.Helper()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("harness condition timed out")
			case <-ticker.C:
			}
		}
	}
	start("ok", "the code")
	var token []byte
	wait(func() bool { token, err = os.ReadFile(filepath.Join(workdir, "ready.txt")); return err == nil })
	prompt, err := os.ReadFile(filepath.Join(workdir, "prompt.txt"))
	if err != nil || !strings.Contains(string(prompt), "Review the code") {
		t.Fatalf("rendered prompt absent: %s (%v)", prompt, err)
	}
	lines := strings.Fields(string(token))
	if len(lines) != 2 {
		t.Fatal("attempt scope was not passed into the harness")
	}
	body := fmt.Sprintf(`{"attempt_id":%q,"token":%q,"output":{"approved":true}}`, lines[0], lines[1])
	res := httptest.NewRecorder()
	runner.submitHarnessResult(res, httptest.NewRequest(http.MethodPost, "/v1/workflows/result", strings.NewReader(body)))
	if res.Code != 200 {
		t.Fatalf("result rejected: %s", res.Body)
	}
	wait(func() bool {
		run, err := st.WorkflowRunByRequest(ctx, "ok")
		return err == nil && run.Status == store.Succeeded && string(run.Outputs) == `{"approved":true}`
	})
	start("fail", "fail case")
	wait(func() bool {
		run, err := st.WorkflowRunByRequest(ctx, "fail")
		return err == nil && run.Status == store.Failed
	})
}
