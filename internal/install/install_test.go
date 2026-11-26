package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLaunchdRunner struct {
	calls     [][]string
	printed   bool
	failBoot  bool
}

func (f *fakeLaunchdRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "print" {
		if f.printed {
			return "running", nil
		}
		return "", errors.New("not loaded")
	}
	if len(args) > 0 && args[0] == "bootstrap" {
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
	_ = os.WriteFile(filepath.Join(b, "Contents/Resources/spec/v1/authoring.md"), []byte("# spec\n"), 0644)
	_ = os.WriteFile(filepath.Join(b, "Contents/Resources/workflows/review-open-prs/workflow.yaml"), []byte("schema: flows/v1\n"), 0644)
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
	userWF := filepath.Join(home, ".local/share/flows/workflows/review-open-prs/workflow.yaml")
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
	raw2, _ := os.ReadFile(userWF)
	if !strings.Contains(string(raw2), "# user customized") {
		t.Fatal("user file lost on failed registration")
	}
}
