package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
	"github.com/romanvolkov/flows/internal/store"
)

func TestImportedToolExecutesThroughDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "flows-import-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("FLOWS_HOME", filepath.Join(dir, "home"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	startDaemon := func() (context.CancelFunc, chan error) {
		daemonCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- localapi.Run(daemonCtx, home, "integration") }()
		return stop, done
	}
	stopDaemon, done := startDaemon()
	stopAndWait := func() {
		stopDaemon()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	defer func() { stopAndWait() }()
	api := client.New(home.Socket())
	defer api.Close()
	eventually := func(check func() bool) {
		t.Helper()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("daemon condition was not met")
			case <-ticker.C:
			}
		}
	}
	eventually(func() bool { _, err := api.Health(ctx); return err == nil })
	write := func(pkg, name, body string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(dir, pkg)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	importPkg := func(path string) {
		t.Helper()
		var result any
		if err := api.JSON(ctx, http.MethodPost, "/v1/library/import", fmt.Sprintf(`{"directory":%q,"allow_overwrite":true}`, path), &result); err != nil {
			t.Fatal(err)
		}
	}
	wf := write("workflow", "workflow.yaml", `schema: flows/v1
id: imported
version: 1.0.0
inputs:
  gate:
    type: string
    required: true
nodes:
  gate:
    uses: core.command
    with:
      command: /bin/sh
      args: [-c, 'while [ ! -f "$1" ]; do sleep 0.05; done', gate, '${{ inputs.gate }}']
  fetch:
    uses: fixture.read
    needs: [gate]
  consume:
    uses: core.command
    needs: [fetch]
    with:
      command: printf
      args: ['%s', '${{ nodes.fetch.outputs.count }}']
outputs:
  count: ${{ nodes.fetch.outputs.count }}
  consumed: ${{ nodes.consume.outputs.stdout }}
`, 0600)
	importPkg(wf)

	list := func() string {
		var result struct{ Workflows []struct{ ID, Digest string } }
		if err := api.JSON(ctx, http.MethodGet, "/v1/workflows", "", &result); err != nil {
			t.Fatal(err)
		}
		for _, workflow := range result.Workflows {
			if workflow.ID == "imported" {
				return workflow.Digest
			}
		}
		return ""
	}
	if list() != "" {
		t.Fatal("workflow with a missing dependency was selectable")
	}
	tool := write("tool", "tool.yaml", `schema: flows/tool/v1
id: fixture
version: 1.0.0
operations:
  - id: fixture.read
    description: Read fixture output
    execution_kind: plugin
`, 0600)
	executable := func(frame string) {
		write("tool", "fixture-plugin", `#!/bin/sh
read -r line
case "$line" in
  *describe*) printf '%s\n' '{"protocol":1,"kind":"manifest","manifest":{"protocol":1,"id":"fixture","version":"1.0.0","operations":[{"id":"fixture.read","description":"Read fixture output"}]}}' ;;
  *) printf '%s\n' '`+frame+`' ;;
esac
`, 0700)
	}
	executable(`{"protocol":1,"kind":"result","output":{"count":42}}`)
	importPkg(tool)
	var digest string
	eventually(func() bool { digest = list(); return digest != "" })
	gate := filepath.Join(dir, "gate-open")
	request := func(id string) store.WorkflowRun {
		t.Helper()
		var result store.WorkflowRun
		body, _ := json.Marshal(map[string]any{"workflow_id": "imported", "request_id": id, "expected_digest": digest, "inputs": map[string]string{"gate": gate}})
		if err := api.JSON(ctx, http.MethodPost, "/v1/workflows/run", string(body), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before, err := plugin.LoadActiveTool(home, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	request("first")
	eventually(func() bool { return request("first").Status == store.Running })
	executable(`{"protocol":1,"kind":"error","error":{"code":"failed","message":"test failure"}}`)
	importPkg(tool)
	eventually(func() bool {
		after, err := plugin.LoadActiveTool(home, "fixture")
		return err == nil && after.Receipt.CacheKey != before.Receipt.CacheKey && list() == digest
	})
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var run store.WorkflowRun
	eventually(func() bool {
		run = request("first")
		return run.Status == store.Succeeded || run.Status == store.Failed
	})
	var outputs map[string]any
	if err := json.Unmarshal(run.Outputs, &outputs); err != nil || run.Status != store.Succeeded || outputs["count"] != float64(42) || strings.TrimSpace(fmt.Sprint(outputs["consumed"])) != "42" {
		t.Fatalf("pinned typed output did not reach the consumer: %+v outputs=%v error=%v", run, outputs, err)
	}
	// Keep checking the real launch endpoint until preparation publishes the new tool.
	var failed store.WorkflowRun
	eventually(func() bool {
		body, _ := json.Marshal(map[string]any{"workflow_id": "imported", "request_id": "failure", "expected_digest": digest, "inputs": map[string]string{"gate": gate}})
		err := api.JSON(ctx, http.MethodPost, "/v1/workflows/run", string(body), &failed)
		return err == nil && (failed.Status == store.Failed || failed.Status == store.Succeeded)
	})
	if failed.Status != store.Failed {
		t.Fatalf("tool error did not fail workflow: %+v", failed)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	interrupted := request("interrupted")
	eventually(func() bool {
		var view store.Workspace
		if err := api.JSON(ctx, http.MethodGet, "/v1/workspace", "", &view); err != nil {
			return false
		}
		for _, node := range view.Nodes {
			if node.RunID == interrupted.RunID && node.Name == "gate" && node.Status == store.Running {
				return true
			}
		}
		return false
	})
	stopAndWait()
	stopDaemon, done = startDaemon()
	eventually(func() bool { _, err := api.Health(ctx); return err == nil })
	if got := request("interrupted"); got.Status != store.Interrupted || got.RunID != interrupted.RunID {
		t.Fatalf("restart did not preserve the interrupted run: %+v", got)
	}

}
