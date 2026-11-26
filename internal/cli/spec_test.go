package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/cli"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
)

func TestInstallSpecValidateExamplesAndBuildOfflineTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Setenv("FLOWS_HOME", filepath.Join(t.TempDir(), "home"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	if err := cli.InstallSpec(home, repoRoot); err != nil {
		t.Fatalf("InstallSpec: %v", err)
	}

	// Verify authoring.md and schema version equality.
	authDoc, err := cli.SpecShow(home, "authoring")
	if err != nil || !strings.Contains(string(authDoc), "flows/v1") || !strings.Contains(string(authDoc), "flows/tool/v1") {
		t.Fatalf("authoring doc missing schema versions: err=%v", err)
	}
	wfSchema, err := cli.SpecShow(home, "workflow")
	if err != nil || !strings.Contains(string(wfSchema), "flows/v1") {
		t.Fatalf("workflow schema missing flows/v1: err=%v", err)
	}

	reg := catalog.NewRegistry()
	if err := reg.RegisterBuiltins(); err != nil {
		t.Fatal(err)
	}

	// Validate every installed workflow example in the isolated home.
	specDir := cli.SpecPath(home, "1")
	for _, ex := range []string{"command-node", "conditional-fanout", "nested-workflow"} {
		rep := cli.ValidateDirectory(filepath.Join(specDir, "examples", ex), reg)
		if !rep.Valid {
			t.Fatalf("installed example %s failed validation: %+v", ex, rep.Diagnostics)
		}
	}

	// Build and invoke the installed sdk-tool offline (GOPROXY=off).
	t.Setenv("GOPROXY", "off")
	sdkToolDir := filepath.Join(specDir, "examples", "sdk-tool")
	if _, err := os.Stat(filepath.Join(sdkToolDir, "tool.yaml")); err != nil {
		t.Fatal(err)
	}
	active, err := plugin.Install(ctx, home, "example-sdk", "1.0.0", sdkToolDir, plugin.InstallOptions{})
	if err != nil {
		t.Fatalf("offline sdk-tool Install: %v", err)
	}
	res, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary:    active.Receipt.BinaryPath,
		Operation: "example-sdk.greet",
		Input:     map[string]string{"name": "flows"},
	})
	if err != nil {
		t.Fatalf("Invoke example-sdk.greet: %v", err)
	}
	var out map[string]string
	if err := json.Unmarshal(res.Output, &out); err != nil || out["greeting"] != "hello, flows" {
		t.Fatalf("unexpected sdk-tool output: %s (err=%v)", string(res.Output), err)
	}
}
