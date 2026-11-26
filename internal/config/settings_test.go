package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
)

func TestSettingsValidationConcurrentSaveManualEditSyntaxAndPinnedPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")

	// 1. Load absent/empty -> defaults.
	s0, err := config.LoadSettings(path)
	if err != nil || s0.DefaultHarness != harness.KindClaude || s0.Revision != 0 {
		t.Fatalf("unexpected default settings: %+v err=%v", s0, err)
	}

	// 2. Unsupported mode & raw secret rejected.
	badMode := s0
	badMode.Harnesses[harness.KindAgy] = config.HarnessSettings{AgyMode: "invalid-mode"}
	if _, err := config.SaveSettings(path, 0, badMode); !errors.Is(err, config.ErrInvalidSettings) {
		t.Fatalf("expected ErrInvalidSettings for bad agy mode, got %v", err)
	}
	rawCred := config.DefaultSettings()
	rawCred.Credentials["github"] = "ghp_raw_secret"
	if _, err := config.SaveSettings(path, 0, rawCred); !errors.Is(err, config.ErrInvalidSettings) {
		t.Fatalf("expected ErrInvalidSettings for raw credential, got %v", err)
	}

	// 3. Concurrent saves at revision 0: exactly one succeeds, one gets ErrRevisionConflict.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			next := config.DefaultSettings()
			next.Concurrency = 4 + idx
			_, errs[idx] = config.SaveSettings(path, 0, next)
		}(i)
	}
	wg.Wait()
	successes := 0
	conflicts := 0
	for _, e := range errs {
		if e == nil {
			successes++
		} else if errors.Is(e, config.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent save error: %v", e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected 1 success and 1 conflict, got successes=%d conflicts=%d", successes, conflicts)
	}

	// 4. Profile precedence & pinned launch policy retained after settings update.
	cur, err := config.LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	perHarness := config.ProfileFromSettings(harness.KindCodex, cur)
	stepProf := harness.Profile{CodexApprovalPolicy: "never"}
	eff := config.ResolveEffectiveProfile(harness.KindCodex, nil, &stepProf, &perHarness)
	if eff.CodexApprovalPolicy != "never" || eff.CodexSandboxMode != "workspace-write" || eff.Source != harness.SourceWorkflowStep {
		t.Fatalf("unexpected effective profile: %+v", eff)
	}

	attemptDir := t.TempDir()
	if err := config.PersistLaunchPolicy(attemptDir, eff, "codex-cli 0.172.0"); err != nil {
		t.Fatal(err)
	}

	// Update settings.yaml; running session's pinned policy remains unchanged.
	cur.Harnesses[harness.KindCodex] = config.HarnessSettings{CodexApprovalPolicy: "untrusted", CodexSandboxMode: "read-only"}
	if _, err := config.SaveSettings(path, cur.Revision, cur); err != nil {
		t.Fatal(err)
	}
	pinned, err := config.LoadLaunchPolicy(attemptDir)
	if err != nil || pinned.Profile.CodexApprovalPolicy != "never" {
		t.Fatalf("pinned policy mutated: %+v err=%v", pinned, err)
	}

	// 5. Manual file edit & syntax error recovery.
	if err := os.WriteFile(path, []byte("version: [broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSettings(path); !errors.Is(err, config.ErrSettingsSyntax) {
		t.Fatalf("expected ErrSettingsSyntax, got %v", err)
	}
	// Saving a valid config recovers cleanly.
	recovered, err := config.SaveSettings(path, 0, config.DefaultSettings())
	if err != nil || recovered.Revision != 1 {
		t.Fatalf("syntax error recovery failed: %+v err=%v", recovered, err)
	}
}
