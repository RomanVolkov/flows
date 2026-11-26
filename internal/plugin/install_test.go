package plugin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
)

func testInstallHome(t *testing.T) paths.Home {
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

func writeVersionedToolSource(t *testing.T, dir, version, reply string) {
	t.Helper()
	sdkDir, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	goMod := "module sampletool\n\ngo 1.26.10\n\nrequire github.com/romanvolkov/flows/sdk/go v0.0.0\n\nreplace github.com/romanvolkov/flows/sdk/go => " + sdkDir + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0600); err != nil {
		t.Fatal(err)
	}
	mainGo := `package main

import (
	"context"
	sdk "github.com/romanvolkov/flows/sdk/go"
)

func main() {
	_ = sdk.Serve(sdk.Manifest{
		ID: "sample",
		Version: "` + version + `",
		Operations: []sdk.Operation{{ID: "sample.ping", Description: "ping"}},
	}, map[string]sdk.Handler{
		"sample.ping": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			return map[string]string{"reply": "` + reply + `"}, nil
		},
	})
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFailedBuildConcurrentMissingCompilerUpgradeAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	home := testInstallHome(t)
	src := filepath.Join(t.TempDir(), "sample")
	writeVersionedToolSource(t, src, "1.0.0", "v1")

	// 1. Missing Go compiler returns ErrMissingGoCompiler.
	if _, err := plugin.Install(ctx, home, "sample", "1.0.0", src, plugin.InstallOptions{
		GoBin: filepath.Join(t.TempDir(), "no-such-go"),
	}); !errors.Is(err, plugin.ErrMissingGoCompiler) {
		t.Fatalf("expected ErrMissingGoCompiler, got %v", err)
	}

	// 2. Concurrent installs of v1 serialize cleanly.
	var wg sync.WaitGroup
	errs := make([]error, 3)
	actives := make([]plugin.ActiveTool, 3)
	for i := range 3 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			actives[idx], errs[idx] = plugin.Install(ctx, home, "sample", "1.0.0", src, plugin.InstallOptions{})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent install %d failed: %v", i, err)
		}
	}
	v1Key := actives[0].Receipt.CacheKey

	// Pin v1 for an active run.
	if err := plugin.PinBuild(home, "sample", v1Key, "run-pinned-1"); err != nil {
		t.Fatal(err)
	}

	// 3. Failed build surfaces CompileError and keeps v1 active.
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc main(){ BROKEN }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := plugin.Install(ctx, home, "sample", "1.1.0", src, plugin.InstallOptions{})
	var compErr *plugin.CompileError
	if !errors.As(err, &compErr) {
		t.Fatalf("expected CompileError, got %v", err)
	}
	cur, err := plugin.LoadActiveTool(home, "sample")
	if err != nil || cur.Receipt.CacheKey != v1Key {
		t.Fatalf("expected v1 to remain active after failed build, got %+v (err=%v)", cur, err)
	}

	// 4. Upgrade to v2 while v1 is pinned; pinned invocation still runs v1, active runs v2.
	writeVersionedToolSource(t, src, "2.0.0", "v2")
	v2, err := plugin.Install(ctx, home, "sample", "2.0.0", src, plugin.InstallOptions{})
	if err != nil {
		t.Fatalf("Install v2: %v", err)
	}
	if v2.Receipt.CacheKey == v1Key {
		t.Fatalf("expected distinct cache key for v2")
	}

	reg := plugin.NewRegistry(home)
	resV1, err := reg.InvokePinned(ctx, "sample", v1Key, plugin.InvokeConfig{Operation: "sample.ping"})
	if err != nil || string(resV1.Output) != `{"reply":"v1"}` {
		t.Fatalf("pinned v1 invocation failed: res=%s err=%v", string(resV1.Output), err)
	}
	resV2, err := reg.InvokePinned(ctx, "sample", "", plugin.InvokeConfig{Operation: "sample.ping"})
	if err != nil || string(resV2.Output) != `{"reply":"v2"}` {
		t.Fatalf("active v2 invocation failed: res=%s err=%v", string(resV2.Output), err)
	}

	// 5. Rollback to v1.
	rb, err := plugin.Rollback(home, "sample", v1Key)
	if err != nil || rb.Receipt.CacheKey != v1Key {
		t.Fatalf("Rollback failed: rb=%+v err=%v", rb, err)
	}
}
