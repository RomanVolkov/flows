package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/paths"
)

func testSettingsHome(t *testing.T) paths.Home {
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

func TestSettingsPanesMixedReadinessInvalidPathReloadErrorAndKeyboard(t *testing.T) {
	ctx := context.Background()
	home := testSettingsHome(t)

	// 1. Authoring guide link detection.
	inv1 := NewToolInventoryPane(home, nil)
	if inv1.AuthoringGuideInstalled {
		t.Fatalf("guide should not be marked installed yet")
	}
	guideDir := filepath.Join(home.Spec(), "v1")
	if err := os.MkdirAll(guideDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guideDir, "authoring.md"), []byte("# Guide"), 0600); err != nil {
		t.Fatal(err)
	}
	inv2 := NewToolInventoryPane(home, nil)
	if !inv2.AuthoringGuideInstalled {
		t.Fatalf("expected AuthoringGuideInstalled=true after staging spec")
	}

	// 2. Import transition + mixed readiness totals.
	toolSrc := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(toolSrc, 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(toolSrc, "tool.yaml"), []byte("schema: flows/tool/v1\nid: demo\nversion: 1.0.0\noperations:\n  - id: demo.ok\n    description: ok\n    execution_kind: plugin\n  - id: demo.auth\n    description: auth\n    execution_kind: plugin\n"), 0600)
	if _, err := inv2.ImportPackage(home, toolSrc, false); err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}

	checker := catalog.NewReadinessChecker()
	specs := []catalog.ToolCheckSpec{
		{
			PackageID:     "demo",
			Enabled:       true,
			ManifestValid: true,
			SchemaValid:   true,
			HandshakeOK:   true,
			Operations: []catalog.OperationCheckSpec{
				{OperationID: "demo.ok"},
				{OperationID: "demo.auth", Credentials: []catalog.CredentialCheck{{Ref: "demo/token", Present: false}}},
			},
		},
	}
	inv2.RefreshReadiness(ctx, checker, specs, nil)
	ready, total := inv2.Totals()
	if ready != 1 || total != 2 {
		t.Fatalf("expected 1/2 ready across mixed readiness, got %d/%d", ready, total)
	}

	// 3. Harness pane invalid path sets ValidationError.
	hp := NewHarnessPane(ctx, config.DefaultSettings())
	hp.SetExecutable(ctx, harness.KindClaude, filepath.Join(t.TempDir(), "missing-claude"))
	if hp.Entries[harness.KindClaude].Available || hp.Entries[harness.KindClaude].ValidationError == "" {
		t.Fatalf("expected invalid path diagnostic, got %+v", hp.Entries[harness.KindClaude])
	}

	// 4. Reload error preserves previous settings intact + keyboard-only config.
	gp := NewGeneralPane(home)
	gp.NextField() // default_directory
	gp.NextField() // concurrency
	gp.StepFocusedValue(2)
	if gp.Settings.Concurrency != 6 {
		t.Fatalf("keyboard step concurrency = %d, want 6", gp.Settings.Concurrency)
	}
	if err := gp.Save(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(home.Settings(), []byte("version: [broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if gp.Reload() {
		t.Fatalf("expected Reload to fail on syntax error")
	}
	if gp.ReloadError == "" || gp.Settings.Concurrency != 6 {
		t.Fatalf("expected previous settings (concurrency=6) preserved on reload error, got %+v err=%q", gp.Settings, gp.ReloadError)
	}
}
