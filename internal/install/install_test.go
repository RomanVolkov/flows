package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeLaunchdRunner struct {
	calls        [][]string
	printed      bool
	failBoot     bool
	unloading    bool
	unloadProbes int
}

func (f *fakeLaunchdRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "print" {
		if f.unloading {
			if f.unloadProbes > 0 {
				f.unloadProbes--
				return "unloading", nil
			}
			f.printed, f.unloading = false, false
		}
		if f.printed {
			return "running", nil
		}
		return "", errors.New("not loaded")
	}
	if len(args) > 0 && args[0] == "bootout" {
		f.unloading = true
	}
	if len(args) > 0 && args[0] == "bootstrap" {
		if f.unloading {
			return "", errors.New("old job is still registered")
		}
		if f.failBoot {
			return "permission denied", errors.New("exit 1")
		}
		f.printed = true
		return "", nil
	}
	return "", nil
}

func makeFakeBundle(t *testing.T) string {
	t.Helper()
	b := filepath.Join(t.TempDir(), "Flows.app")
	for _, dir := range []string{
		"Contents/MacOS",
		"Contents/Resources/spec/v1",
		"Contents/Resources/workflows/review-open-prs",
	} {
		if err := os.MkdirAll(filepath.Join(b, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, bin := range []string{"flows", "flowsd", "flows-host"} {
		if err := os.WriteFile(filepath.Join(b, "Contents/MacOS", bin), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b, "Contents/Resources/spec/v1/authoring.md"), []byte("# spec\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "Contents/Resources/workflows/review-open-prs/workflow.yaml"), []byte("schema: flows/v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallTwiceSpacesAndUserEditedWorkflow(t *testing.T) {
	bundle := makeFakeBundle(t)
	home := filepath.Join(t.TempDir(), "User Home With Spaces")
	runner := &fakeLaunchdRunner{}

	m, err := Install(context.Background(), Options{
		HomeDir:      home,
		SourceBundle: bundle,
		IsDevHome:    true,
		Runner:       runner,
		HealthCheck:  func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if !strings.HasPrefix(m.Label, "dev.flows.daemon.") {
		t.Fatalf("expected isolated dev label, got %q", m.Label)
	}

	// User edits a workflow file; second install must preserve it.
	userWF := filepath.Join(home, ".config/flows/workflows/review-open-prs/workflow.yaml")
	if err := os.WriteFile(userWF, []byte("schema: flows/v1\n# user customized\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), Options{
		HomeDir:      home,
		SourceBundle: bundle,
		IsDevHome:    true,
		Runner:       runner,
		HealthCheck:  func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("second install: %v", err)
	}
	raw, err := os.ReadFile(userWF)
	if err != nil || !strings.Contains(string(raw), "# user customized") {
		t.Fatalf("user-edited workflow was overwritten: %q", string(raw))
	}

	// Failed registration reports failing step and preserves preexisting files.
	runnerFail := &fakeLaunchdRunner{failBoot: true}
	_, err = Install(context.Background(), Options{
		HomeDir:      home,
		SourceBundle: bundle,
		IsDevHome:    true,
		Runner:       runnerFail,
	})
	if err == nil || !strings.Contains(err.Error(), "step register launchd") {
		t.Fatalf("expected step register launchd failure, got %v", err)
	}
	raw2, err := os.ReadFile(userWF)
	if err != nil || !strings.Contains(string(raw2), "# user customized") {
		t.Fatal("user file lost on failed registration")
	}

	// Failed HealthCheck reports step daemon health check.
	if _, err := Install(context.Background(), Options{
		HomeDir:      home,
		SourceBundle: bundle,
		IsDevHome:    true,
		Runner:       runner,
		HealthCheck:  func(context.Context) error { return errors.New("protocol mismatch") },
	}); err == nil || !strings.Contains(err.Error(), "step daemon health check") {
		t.Fatalf("expected health check error, got %v", err)
	}

	// Preexisting CLI link conflict is rejected without clobbering.
	cliLink := filepath.Join(home, ".local/bin/flows")
	if err := os.Remove(cliLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/local/bin/foreign-flows", cliLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), Options{
		HomeDir:      home,
		SourceBundle: bundle,
		IsDevHome:    true,
		Runner:       runner,
	}); err == nil || !strings.Contains(err.Error(), "step check cli link") {
		t.Fatalf("expected cli link conflict error, got %v", err)
	}
}

func TestRegisterLaunchdRestartsWithoutUnloadingUnchangedJob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.flows.test.plist")
	cfg := LaunchdConfig{Label: "dev.flows.test", DaemonBin: "/path/flowsd", LogDir: dir}
	runner := &fakeLaunchdRunner{}
	if err := RegisterLaunchd(t.Context(), runner, path, cfg); err != nil {
		t.Fatal(err)
	}
	target := runner.calls[0][1]
	runner.calls = nil
	cfg.Restart = true
	if err := RegisterLaunchd(t.Context(), runner, path, cfg); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"print", target}, {"kickstart", "-k", target}}; !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("restart calls = %v, want %v", runner.calls, want)
	}
	runner.calls = nil
	runner.unloadProbes = 1
	cfg.DaemonBin = "/new/path/flowsd"
	if err := RegisterLaunchd(t.Context(), runner, path, cfg); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"print", target}, {"bootout", target}, {"print", target}, {"print", target},
		{"bootstrap", filepath.Dir(target), path}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("reload calls = %v, want %v", runner.calls, want)
	}
}
