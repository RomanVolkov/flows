package localapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romanvolkov/flows/internal/store"
)

func TestWorkflowLaunchValidation(t *testing.T) {
	home := libraryTestHome(t)
	dir := filepath.Join(home.Workflows(), "sample")
	writePackageFile(t, dir, "workflow.yaml", `schema: flows/v1
id: sample
version: 1.0.0
inputs:
  count:
    type: integer
    required: true
nodes:
  run:
    uses: core.command
    with: {command: /usr/bin/true}
`)
	worker := newLibraryReconciler(home)
	worker.reconcile(t.Context())
	state, err := store.Open(t.Context(), filepath.Join(home.State, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runner := &workflowRunner{ctx: ctx, state: state, library: worker, cancellations: make(map[string]context.CancelFunc)}
	defer runner.wg.Wait()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		resp := httptest.NewRecorder()
		runner.serveHTTP(resp, httptest.NewRequest(method, path, strings.NewReader(body)))
		return resp
	}
	list := call(http.MethodGet, "/v1/workflows", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"type":"integer"`) {
		t.Fatalf("listing omitted ready typed workflow: %s", list.Body)
	}
	digest := worker.snapshot().Entries[0].Digest
	tests := []struct {
		body   string
		status int
	}{
		{`{}`, http.StatusBadRequest},
		{`{"request_id":"one","workflow_id":"sample","expected_digest":"old"}`, http.StatusConflict},
		{fmt.Sprintf(`{"request_id":"one","workflow_id":"sample","expected_digest":%q,"inputs":{"count":"wrong"}}`, digest), http.StatusUnprocessableEntity},
		{fmt.Sprintf(`{"request_id":"one","workflow_id":"sample","expected_digest":%q,"inputs":{"count":2}}`, digest), http.StatusOK},
		{fmt.Sprintf(`{"request_id":"one","workflow_id":"sample","expected_digest":%q,"inputs":{"count":2}}`, digest), http.StatusOK},
	}
	for _, test := range tests {
		resp := call(http.MethodPost, "/v1/workflows/run", test.body)
		if resp.Code != test.status {
			t.Fatalf("%s: got %d, want %d: %s", test.body, resp.Code, test.status, resp.Body)
		}
	}
	view, err := state.Snapshot(ctx)
	if err != nil || len(view.Runs) != 1 {
		t.Fatalf("duplicate request created more than one run: %+v, %v", view, err)
	}
}
