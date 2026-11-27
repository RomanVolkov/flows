package localapi

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
	"github.com/romanvolkov/flows/internal/workflow"
)

type preparedTool struct {
	entry  library.IndexEntry
	active plugin.ActiveTool
	pkg    catalog.PackageProvenance
	err    error
}

type libraryReconciler struct {
	home paths.Home
	wake chan struct{}

	mu       sync.RWMutex
	index    library.IndexSnapshot
	registry *catalog.Registry
	tools    map[string]preparedTool
	lastScan *library.IndexSnapshot

	prepareTool func(context.Context, library.IndexEntry) preparedTool
}

func newLibraryReconciler(home paths.Home) *libraryReconciler {
	w := &libraryReconciler{
		home:  home,
		wake:  make(chan struct{}, 1),
		index: library.IndexSnapshot{Entries: []library.IndexEntry{}},
		tools: make(map[string]preparedTool),
	}
	w.prepareTool = w.buildTool
	w.trigger()
	return w
}

func (w *libraryReconciler) trigger() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *libraryReconciler) snapshot() library.IndexSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.index
}

func (w *libraryReconciler) run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		w.reconcile(ctx)
	}
}

func (w *libraryReconciler) reconcile(ctx context.Context) {
	scan, err := library.Scan(w.home, nil)
	if err != nil || reflect.DeepEqual(w.lastScan, scan) || ctx.Err() != nil {
		return
	}
	pending := *scan
	pending.Entries = append([]library.IndexEntry{}, scan.Entries...)
	for i := range pending.Entries {
		pending.Entries[i].Status = "preparing"
	}
	w.mu.Lock()
	previous := w.index
	w.index = pending
	w.mu.Unlock()

	final := *scan
	final.Entries = append([]library.IndexEntry{}, scan.Entries...)
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	tools := make(map[string]preparedTool)
	workflows := make(map[string]library.IndexEntry)
	for i := range final.Entries {
		entry := &final.Entries[i]
		if entry.Kind == library.KindWorkflow {
			workflows[entry.ID] = *entry
			continue
		}
		result, ok := w.tools[entry.ID]
		if !ok || result.entry.Digest != entry.Digest || result.entry.Path != entry.Path {
			result = w.prepareTool(ctx, *entry)
		}
		if result.err == nil {
			result.err = reg.RegisterPackage(result.pkg)
		}
		tools[entry.ID] = result
		setPreparation(entry, "tool_prepare_failed", result.err)
	}
	for i := range final.Entries {
		entry := &final.Entries[i]
		if entry.Kind != library.KindWorkflow {
			continue
		}
		def, diags := workflow.ParseFile(filepath.Join(entry.Path, "workflow.yaml"))
		if len(diags) == 0 {
			_, diags = workflow.CompileWithLoader(def, reg, libraryWorkflowLoader(workflows))
		}
		entry.Status = "ready"
		if len(diags) > 0 {
			entry.Status = "invalid"
			entry.Diagnostics = diags
		}
	}

	current, err := library.Scan(w.home, nil)
	if err != nil || !reflect.DeepEqual(scan, current) {
		w.mu.Lock()
		w.index = previous
		w.mu.Unlock()
		w.trigger()
		return
	}
	if ctx.Err() != nil {
		return
	}
	w.mu.Lock()
	w.index, w.registry, w.tools = final, reg, tools
	w.lastScan = scan
	w.mu.Unlock()
}

func setPreparation(entry *library.IndexEntry, code string, err error) {
	entry.Status = "ready"
	if err != nil {
		entry.Status = "invalid"
		entry.Diagnostics = []workflow.Diagnostic{{File: entry.Path, Code: code, Message: err.Error(), Severity: "error"}}
	}
}

func libraryWorkflowLoader(entries map[string]library.IndexEntry) workflow.RelativeWorkflowLoader {
	return func(parent *workflow.Definition, ref string) (*workflow.Definition, error) {
		path := ""
		if entry, ok := entries[ref]; ok {
			path = filepath.Join(entry.Path, "workflow.yaml")
		} else {
			if !filepath.IsLocal(ref) {
				return nil, fmt.Errorf("invalid workflow reference %q", ref)
			}
			path = filepath.Join(filepath.Dir(parent.File), ref)
			if filepath.Ext(path) == "" {
				path = filepath.Join(path, "workflow.yaml")
			}
		}
		def, diags := workflow.ParseFile(path)
		if len(diags) > 0 {
			return nil, diags[0]
		}
		return def, nil
	}
}

func (w *libraryReconciler) buildTool(ctx context.Context, entry library.IndexEntry) (result preparedTool) {
	result.entry = entry
	pkg, err := library.ParseToolManifest(entry.Path, entry.Origin, entry.Digest, true)
	if err != nil {
		result.err = err
		return
	}
	result.pkg = pkg
	hasPlugin := false
	for _, op := range pkg.Operations {
		hasPlugin = hasPlugin || op.ExecutionKind == catalog.ExecPlugin
	}
	if !hasPlugin {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	active, err := plugin.LoadActiveTool(w.home, entry.ID)
	if err != nil || active.PackageDigest != entry.Digest {
		active, err = w.activateTool(ctx, entry)
	}
	if err == nil {
		manifestOps := make(map[string]bool)
		manifest, describeErr := plugin.Describe(ctx, active.Receipt.BinaryPath)
		err = describeErr
		if err == nil {
			for _, op := range manifest.Operations {
				manifestOps[op.ID] = true
			}
			for _, op := range pkg.Operations {
				if op.ExecutionKind == catalog.ExecPlugin && !manifestOps[op.ID] {
					err = fmt.Errorf("tool executable does not provide %q", op.ID)
					break
				}
			}
		}
	}
	result.active, result.err = active, err
	return
}

func (w *libraryReconciler) activateTool(ctx context.Context, entry library.IndexEntry) (plugin.ActiveTool, error) {
	binary := filepath.Join(entry.Path, entry.ID+"-plugin")
	if info, err := os.Stat(binary); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0100 != 0 {
		err := plugin.ActivatePrebuilt(ctx, w.home, entry.ID, entry.Version, entry.Path, binary, entry.Digest)
		if err != nil {
			return plugin.ActiveTool{}, err
		}
		return plugin.LoadActiveTool(w.home, entry.ID)
	}
	hasSource := false
	err := filepath.WalkDir(entry.Path, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (d.Name() == "go.mod" || strings.HasSuffix(d.Name(), ".go")) {
			hasSource = true
			return fs.SkipAll
		}
		return err
	})
	if err != nil {
		return plugin.ActiveTool{}, err
	}
	if !hasSource {
		return plugin.ActiveTool{}, fmt.Errorf("tool %q declares plugin operations but has no Go source or executable", entry.ID)
	}
	return plugin.Install(ctx, w.home, entry.ID, entry.Version, entry.Path, plugin.InstallOptions{PackageDigest: entry.Digest})
}
