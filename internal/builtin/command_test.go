package builtin_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/desktop/launcher"
	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/workflow"
)

func TestWorkflowFormCommandTimeoutDeepLinkAndExactlyOnceAnswer(t *testing.T) {
	ctx := context.Background()

	// 1. Workflow form: no-input launch, missing required field, duplicate request.
	def, diags := workflow.ParseBytes("workflow.yaml", []byte(`schema: flows/v1
id: sample-flow
version: 1.0.0
inputs:
  repo: {type: string, required: true}
nodes:
  step1:
    uses: core.command
`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	form := launcher.NewWorkflowForm(def, "run-prev")
	wl := launcher.NewWorkflowLauncher()

	// Missing required field fails with field-level diagnostic.
	if _, err := wl.Launch(ctx, def, &form, map[string]any{}, "req-1"); err == nil || form.Fields[0].Error == "" {
		t.Fatalf("expected missing field diagnostic, got err=%v field=%+v", err, form.Fields)
	}

	// Valid launch + duplicate request deduplication.
	res1, err := wl.Launch(ctx, def, &form, map[string]any{"repo": "flows"}, "req-2")
	if err != nil || len(res1.FirstReachable) != 1 || res1.FirstReachable[0] != "step1" {
		t.Fatalf("Launch failed: res=%+v err=%v", res1, err)
	}
	res2, err := wl.Launch(ctx, def, &form, map[string]any{"repo": "flows"}, "req-2")
	if err != nil || res2.RunID != res1.RunID {
		t.Fatalf("duplicate launch request created new run: %+v vs %+v", res1, res2)
	}

	// No-input workflow launch.
	noInDef, _ := workflow.ParseBytes("workflow.yaml", []byte("schema: flows/v1\nid: no-in\nversion: 1.0.0\nnodes:\n  a:\n    uses: core.command\n"))
	noInForm := launcher.NewWorkflowForm(noInDef, "")
	if _, err := wl.Launch(ctx, noInDef, &noInForm, nil, "req-noin"); err != nil {
		t.Fatalf("no-input launch failed: %v", err)
	}

	// 2. Command execution with streaming & timeout.
	var streamed []string
	cmdRes, err := builtin.RunCommand(ctx, builtin.CommandSpec{
		Executable: "/bin/sh",
		Args:       []string{"-c", "echo line1; echo line2"},
		OnOutput: func(stream, line string) {
			streamed = append(streamed, stream+":"+line)
		},
	})
	if err != nil || cmdRes.ExitCode != 0 || len(streamed) != 2 {
		t.Fatalf("RunCommand failed: res=%+v streamed=%v err=%v", cmdRes, streamed, err)
	}

	toRes, toErr := builtin.RunCommand(ctx, builtin.CommandSpec{
		Executable: "/bin/sleep",
		Args:       []string{"5"},
		Timeout:    80 * time.Millisecond,
	})
	if !errors.Is(toErr, context.DeadlineExceeded) || !toRes.TimedOut {
		t.Fatalf("expected timeout, got res=%+v err=%v", toRes, toErr)
	}

	// 3. Notifications, deep link, and exactly-once question answering.
	nc, err := builtin.NewNotificationCenter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q, noti, err := nc.AskAndNotify("run-9", "node-4", "att-7", "Approve merge?", []string{"yes", "no"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(noti.DeepLink, "flows://") {
		t.Fatalf("unexpected deep link: %q", noti.DeepLink)
	}
	runID, nodeID, err := builtin.ResolveDeepLink(noti.DeepLink)
	if err != nil || runID != "run-9" || nodeID != "node-4" {
		t.Fatalf("ResolveDeepLink failed: %s %s %v", runID, nodeID, err)
	}

	answered, err := nc.AnswerOnce(q.ID, "yes")
	if err != nil || answered.AttemptID != "att-7" || answered.Answer != "yes" {
		t.Fatalf("AnswerOnce failed: %+v err=%v", answered, err)
	}
	if _, err := nc.AnswerOnce(q.ID, "no"); !errors.Is(err, builtin.ErrAlreadyAnswered) {
		t.Fatalf("expected ErrAlreadyAnswered on second answer, got %v", err)
	}
}
