package localapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

const prepareWorkflow = `schema: flows/v1
id: sample
version: 1.0.0
nodes:
  inspect:
    uses: sample-tool.inspect
`

const prepareTool = `schema: flows/tool/v1
id: sample-tool
version: 1.0.0
operations:
  - id: sample-tool.inspect
    description: Inspect a fixture
    execution_kind: plugin
`

func writePackageFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func packageStatus(w *libraryReconciler, id string) string {
	for _, entry := range w.snapshot().Entries {
		if entry.ID == id {
			return entry.Status
		}
	}
	return ""
}

func TestLibraryReconcilerImportAndDependencyChanges(t *testing.T) {
	ctx := context.Background()
	home := libraryTestHome(t)
	worker := newLibraryReconciler(home)
	<-worker.wake
	source := t.TempDir()
	writePackageFile(t, source, "workflow.yaml", prepareWorkflow)
	request := httptest.NewRequest(http.MethodPost, "/v1/library/import", strings.NewReader(fmt.Sprintf(`{"directory":%q}`, source)))
	response := httptest.NewRecorder()
	handleLibrary(response, request, home, worker)
	if response.Code != http.StatusOK {
		t.Fatalf("import returned %d: %s", response.Code, response.Body)
	}
	select {
	case <-worker.wake:
	default:
		t.Fatal("import did not schedule preparation")
	}

	worker.reconcile(ctx)
	if got := packageStatus(worker, "sample"); got != "invalid" {
		t.Fatalf("workflow without dependency = %q", got)
	}
	toolDir := filepath.Join(home.Tools(), "sample-tool")
	writePackageFile(t, toolDir, "tool.yaml", prepareTool)
	worker.reconcile(ctx)
	if got := packageStatus(worker, "sample-tool"); got != "invalid" {
		t.Fatalf("tool without executable = %q", got)
	}

	calls := 0
	worker.prepareTool = func(_ context.Context, entry library.IndexEntry) preparedTool {
		calls++
		pkg, err := library.ParseToolManifest(entry.Path, entry.Origin, entry.Digest, true)
		return preparedTool{entry: entry, pkg: pkg, err: err}
	}
	writePackageFile(t, toolDir, "fix.txt", "updated revision")
	worker.reconcile(ctx)
	if got := packageStatus(worker, "sample"); got != "ready" {
		t.Fatalf("workflow with repaired dependency = %q", got)
	}
	worker.reconcile(ctx)
	if calls != 1 {
		t.Fatalf("prepared unchanged tool %d times", calls)
	}
	if err := os.Remove(filepath.Join(toolDir, "tool.yaml")); err != nil {
		t.Fatal(err)
	}
	worker.reconcile(ctx)
	if got := packageStatus(worker, "sample"); got != "invalid" {
		t.Fatalf("workflow after dependency removal = %q", got)
	}
}

func TestLibraryReconcilerDiscardsSupersededResults(t *testing.T) {
	home := libraryTestHome(t)
	worker := newLibraryReconciler(home)
	dir := filepath.Join(home.Tools(), "sample-tool")
	writePackageFile(t, dir, "tool.yaml", prepareTool)
	changed := false
	worker.prepareTool = func(_ context.Context, entry library.IndexEntry) preparedTool {
		if !changed {
			changed = true
			writePackageFile(t, dir, "tool.yaml", "malformed: [")
		}
		pkg, err := library.ParseToolManifest(entry.Path, entry.Origin, entry.Digest, true)
		return preparedTool{entry: entry, pkg: pkg, err: err}
	}
	worker.reconcile(context.Background())
	if got := packageStatus(worker, "sample-tool"); got != "" {
		t.Fatalf("superseded state published as %q", got)
	}
	worker.reconcile(context.Background())
	snapshot := worker.snapshot()
	if len(snapshot.Malformed) != 1 || len(snapshot.Malformed[0].Diagnostics) == 0 {
		t.Fatalf("missing malformed package diagnostic: %+v", snapshot)
	}
}

func libraryTestHome(t *testing.T) paths.Home {
	t.Helper()
	t.Setenv("FLOWS_HOME", filepath.Join(t.TempDir(), "home"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	return home
}
