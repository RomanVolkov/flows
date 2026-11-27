package library

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/plugin"
)

func copyFlatDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func makeBundledResourcesFixture(t *testing.T) string {
	t.Helper()
	res := filepath.Join(t.TempDir(), "Resources")
	if err := os.MkdirAll(filepath.Join(res, "workflows"), 0700); err != nil {
		t.Fatal(err)
	}
	copyFlatDir(t, "../../sdk/go", filepath.Join(res, "spec", "v1", "sdk", "go"))

	// Bundle real git plugin source so self-contained compilation is also verified offline.
	gitDst := filepath.Join(res, "plugins", "git")
	if err := os.MkdirAll(filepath.Join(gitDst, "cmd", "plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	toolYAML, err := os.ReadFile("../../plugins/git/tool.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDst, "tool.yaml"), toolYAML, 0600); err != nil {
		t.Fatal(err)
	}
	copyFlatDir(t, "../../plugins/git/cmd/plugin", filepath.Join(gitDst, "cmd", "plugin"))
	goMod := "module flows-plugin-git\n\ngo 1.26.10\n\nrequire github.com/romanvolkov/flows/sdk/go v0.0.0\n\nreplace github.com/romanvolkov/flows/sdk/go => ../../spec/v1/sdk/go\n"
	if err := os.WriteFile(filepath.Join(gitDst, "go.mod"), []byte(goMod), 0600); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestInstallBundledCompilesPluginFromSourceOffline(t *testing.T) {
	home := testHome(t)
	res := makeBundledResourcesFixture(t)

	t.Setenv("GOPROXY", "off")
	if err := InstallBundled(home, res); err != nil {
		t.Fatalf("InstallBundled: %v", err)
	}

	active, err := plugin.LoadActiveTool(home, "git")
	if err != nil {
		t.Fatalf("LoadActiveTool(git): %v", err)
	}
	if !active.Bundled {
		t.Fatalf("expected active.Bundled=true")
	}
	if strings.HasPrefix(active.Receipt.CacheKey, "bundle-") || active.Receipt.GoVersion == "" {
		t.Fatalf("expected source build receipt, got %+v", active.Receipt)
	}
	if _, err := os.Stat(active.Receipt.BinaryPath); err != nil {
		t.Fatalf("expected compiled binary at %s: %v", active.Receipt.BinaryPath, err)
	}
	// Verify compiled source lives in editable home.Tools().
	if _, err := os.Stat(filepath.Join(home.Tools(), "git", "cmd", "plugin", "main.go")); err != nil {
		t.Fatalf("expected copied source under home.Tools(): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manifest, err := plugin.Describe(ctx, active.Receipt.BinaryPath)
	if err != nil || manifest.ID != "git" {
		t.Fatalf("Describe compiled git plugin: manifest=%+v err=%v", manifest, err)
	}
}

func TestInstallBundledRequiresGoCompiler(t *testing.T) {
	home := testHome(t)
	res := makeBundledResourcesFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := installBundled(ctx, home, res, filepath.Join(t.TempDir(), "missing-go"))
	if !errors.Is(err, plugin.ErrMissingGoCompiler) {
		t.Fatalf("expected ErrMissingGoCompiler, got %v", err)
	}
}

func TestInstallBundledUpgradesLegacyPrebuiltAndPreservesUserEdits(t *testing.T) {
	home := testHome(t)
	res := makeBundledResourcesFixture(t)

	// Simulate an older prebuilt installation (tool.yaml + legacy binary, empty marker).
	legacyToolDir := filepath.Join(home.Tools(), "git")
	if err := os.MkdirAll(legacyToolDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyToolDir, "tool.yaml"), []byte("schema: flows/tool/v1\nid: git\nversion: 0.9.0\nowner: flows\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyToolDir, "git-plugin"), []byte("prebuilt"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(home, legacyToolDir, ImportOptions{AllowOverwrite: true}); err != nil {
		t.Fatal(err)
	}
	installedDir := filepath.Join(home.State, "installed", "git")
	if err := os.MkdirAll(installedDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyActive, _ := json.Marshal(plugin.ActiveTool{
		Bundled:   true,
		ToolID:    "git",
		SourceDir: legacyToolDir,
		Receipt:   plugin.BuildReceipt{ToolID: "git", CacheKey: "bundle-legacy"},
	})
	if err := os.WriteFile(filepath.Join(installedDir, "active.json"), legacyActive, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.State, "bundled-library-installed"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOPROXY", "off")
	if err := InstallBundled(home, res); err != nil {
		t.Fatalf("upgrade InstallBundled: %v", err)
	}
	upgraded, err := plugin.LoadActiveTool(home, "git")
	if err != nil || strings.HasPrefix(upgraded.Receipt.CacheKey, "bundle-") {
		t.Fatalf("expected upgraded source build, got %+v err=%v", upgraded, err)
	}
	if _, err := os.Stat(filepath.Join(home.Tools(), "git", "git-plugin")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy prebuilt binary removed from editable tools dir")
	}

	// Simulate user editing the Go tool source and running InstallBundled again with reset marker.
	customNote := filepath.Join(home.Tools(), "git", "CUSTOM_EDIT.go")
	if err := os.WriteFile(customNote, []byte("// user customization\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home.State, "bundled-library-installed")); err != nil {
		t.Fatal(err)
	}
	if err := InstallBundled(home, res); err != nil {
		t.Fatalf("re-run InstallBundled: %v", err)
	}
	if _, err := os.Stat(customNote); err != nil {
		t.Fatalf("expected user-modified tool source preserved: %v", err)
	}
}

func TestInstallBundledUsesPrebuiltBinaryWhenPresentWithoutGoCompiler(t *testing.T) {
	home := testHome(t)
	res := makeBundledResourcesFixture(t)

	// Compile git plugin once into an executable bundled binary inside Resources/plugins/git/git-plugin.
	tmpHome := testHome(t)
	if err := InstallBundled(tmpHome, res); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	seeded, err := plugin.LoadActiveTool(tmpHome, "git")
	if err != nil {
		t.Fatal(err)
	}
	binBytes, err := os.ReadFile(seeded.Receipt.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res, "plugins", "git", "git-plugin"), binBytes, 0700); err != nil {
		t.Fatal(err)
	}

	// Even without a Go compiler, a bundle containing a prebuilt executable binary activates directly.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := installBundled(ctx, home, res, filepath.Join(t.TempDir(), "missing-go")); err != nil {
		t.Fatalf("installBundled prebuilt without go compiler: %v", err)
	}
	active, err := plugin.LoadActiveTool(home, "git")
	if err != nil {
		t.Fatalf("LoadActiveTool(git): %v", err)
	}
	if !strings.HasPrefix(active.Receipt.CacheKey, "bundle-") {
		t.Fatalf("expected bundle-* cache key for prebuilt binary, got %+v", active.Receipt)
	}
	manifest, err := plugin.Describe(ctx, active.Receipt.BinaryPath)
	if err != nil || manifest.ID != "git" {
		t.Fatalf("Describe prebuilt git plugin: manifest=%+v err=%v", manifest, err)
	}
}
