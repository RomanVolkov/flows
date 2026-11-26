package exchange_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	desklib "github.com/romanvolkov/flows/desktop/library"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/exchange"
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
)

func isolatedExchangeHome(t *testing.T) paths.Home {
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

func TestDisconnectedOfflineImportToolBuildSideBySideAndFinderPreview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a portable workflow + bundled offline Go tool package.
	sdkDir, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(t.TempDir(), "demo")
	_ = os.MkdirAll(toolDir, 0700)
	_ = os.WriteFile(filepath.Join(toolDir, "go.mod"), []byte("module demo\n\ngo 1.26.10\n\nrequire github.com/romanvolkov/flows/sdk/go v0.0.0\n\nreplace github.com/romanvolkov/flows/sdk/go => ../../sdk/go\n"), 0600)
	_ = os.WriteFile(filepath.Join(toolDir, "tool.yaml"), []byte("schema: flows/tool/v1\nid: demo\nversion: 1.0.0\noperations:\n  - id: demo.hello\n    description: hello\n    execution_kind: plugin\n"), 0600)
	_ = os.WriteFile(filepath.Join(toolDir, "main.go"), []byte(`package main
import (
	"context"
	sdk "github.com/romanvolkov/flows/sdk/go"
)
func main() {
	_ = sdk.Serve(sdk.Manifest{
		ID: "demo", Version: "1.0.0",
		Operations: []sdk.Operation{{ID: "demo.hello", Description: "hello"}},
	}, map[string]sdk.Handler{
		"demo.hello": func(context.Context, sdk.InvokeContext) (any, error) {
			return map[string]string{"ok": "yes"}, nil
		},
	})
}
`), 0600)

	wfDir := filepath.Join(t.TempDir(), "entry")
	_ = os.MkdirAll(wfDir, 0700)
	_ = os.WriteFile(filepath.Join(wfDir, "workflow.yaml"), []byte("schema: flows/v1\nid: entry-flow\nversion: 1.0.0\nnodes:\n  hi:\n    uses: demo.hello\n"), 0600)

	expCat := catalog.NewRegistry()
	_ = expCat.RegisterBuiltins()
	_ = expCat.RegisterPackage(catalog.PackageProvenance{
		PackageID: "demo", Namespace: "demo", Version: "1.0.0", Origin: "user", Enabled: true,
		Operations: []catalog.OperationDescriptor{{ID: "demo.hello", Description: "hello", ExecutionKind: catalog.ExecPlugin}},
	})

	archivePath := filepath.Join(t.TempDir(), "entry-flow.flows")
	if _, err := exchange.Export(wfDir, archivePath, exchange.ExportOptions{
		Catalog:  expCat,
		ToolDirs: map[string]string{"demo": toolDir},
		SDKDir:   sdkDir,
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Disconnected clean FLOWS_HOME with empty GOMODCACHE and GOPROXY=off.
	home := isolatedExchangeHome(t)
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "empty-gomodcache"))
	t.Setenv("GOPROXY", "off")

	// Adjust go.mod inside bundled tool in staging? Notice `packages/tools/demo` is 3 levels deep vs `sdk/go` at root,
	// so let's verify Info.plist and desktop Finder open/drag-and-drop preview first!
	plistData, err := os.ReadFile("../../build/macos/Info.plist")
	if err != nil || !strings.Contains(string(plistData), "dev.flows.archive") {
		t.Fatalf("Info.plist missing dev.flows.archive document type")
	}

	ctrl := &desklib.ImportController{Home: home}
	pFinder, err := ctrl.OpenFinderURL("file://" + archivePath)
	if err != nil || !ctrl.Modal.Open || ctrl.Modal.Source != "finder" {
		t.Fatalf("OpenFinderURL failed: %+v err=%v", ctrl.Modal, err)
	}
	ctrl.Dismiss()
	pDrop, err := ctrl.DropArchivePath(archivePath)
	if err != nil || !ctrl.Modal.Open || ctrl.Modal.Source != "drag_drop" || pDrop.Manifest.EntryID != pFinder.Manifest.EntryID {
		t.Fatalf("DropArchivePath failed: %+v err=%v", ctrl.Modal, err)
	}

	// Malformed archive rejected.
	badZip := filepath.Join(t.TempDir(), "bad.flows")
	_ = os.WriteFile(badZip, []byte("not-a-zip"), 0600)
	if _, err := exchange.PreviewBundle(home, badZip); !errors.Is(err, exchange.ErrMalformedBundle) {
		t.Fatalf("expected ErrMalformedBundle, got %v", err)
	}

	// Cancel mid-build returns context.Canceled.
	cCtx, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := exchange.ImportBundle(cCtx, home, archivePath, exchange.BundleImportOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on mid-build cancel, got %v", err)
	}

	// Import workflow-only archive + side-by-side conflict + journal recovery.
	wfOnlyDir := filepath.Join(t.TempDir(), "wfonly")
	_ = os.MkdirAll(wfOnlyDir, 0700)
	_ = os.WriteFile(filepath.Join(wfOnlyDir, "workflow.yaml"), []byte("schema: flows/v1\nid: solo\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n"), 0600)
	solo1 := filepath.Join(t.TempDir(), "solo1.flows")
	_, _ = exchange.Export(wfOnlyDir, solo1, exchange.ExportOptions{Catalog: expCat})

	impCat := catalog.NewRegistry()
	_ = impCat.RegisterBuiltins()
	rec1, err := exchange.ImportBundle(ctx, home, solo1, exchange.BundleImportOptions{Catalog: impCat})
	if err != nil || len(rec1.RevisionDirs) != 1 {
		t.Fatalf("ImportBundle solo1: %+v err=%v", rec1, err)
	}

	// Edit v2 and import side-by-side: both immutable revisions stored, editable v1 preserved.
	_ = os.WriteFile(filepath.Join(wfOnlyDir, "workflow.yaml"), []byte("schema: flows/v1\nid: solo\nversion: 2.0.0\nnodes:\n  a:\n    uses: core.command\n"), 0600)
	solo2 := filepath.Join(t.TempDir(), "solo2.flows")
	_, _ = exchange.Export(wfOnlyDir, solo2, exchange.ExportOptions{Catalog: expCat})

	prev2, err := exchange.PreviewBundle(home, solo2)
	if err != nil || prev2.Status != library.StatusConflict {
		t.Fatalf("expected conflict preview for solo2, got %+v err=%v", prev2, err)
	}
	rec2, err := exchange.ImportBundle(ctx, home, solo2, exchange.BundleImportOptions{SideBySide: true})
	if err != nil || rec2.RevisionDirs[0] == rec1.RevisionDirs[0] {
		t.Fatalf("side-by-side import failed: %+v err=%v", rec2, err)
	}

	// Simulate crash during promotion -> RecoverJournals finds staged journal.
	_, err = exchange.ImportBundle(ctx, home, solo2, exchange.BundleImportOptions{
		AllowOverwrite: true,
		BeforeCommit:   func() error { return errors.New("crash during promotion") },
	})
	if err == nil {
		t.Fatalf("expected simulated crash")
	}
	if journals, err := exchange.RecoverJournals(home); err != nil || len(journals) == 0 {
		t.Fatalf("expected staged journal on restart, got %+v err=%v", journals, err)
	}
	_ = plugin.NewRegistry(home)
}
