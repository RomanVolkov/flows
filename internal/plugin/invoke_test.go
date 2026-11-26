package plugin_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/plugin"
	sdk "github.com/romanvolkov/flows/sdk/go"
)

func buildFixturePlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sdkDir, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}

	goMod := "module fixtureplugin\n\ngo 1.26.10\n\nrequire github.com/romanvolkov/flows/sdk/go v0.0.0\n\nreplace github.com/romanvolkov/flows/sdk/go => " + sdkDir + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0600); err != nil {
		t.Fatal(err)
	}

	mainGo := `package main

import (
	"context"
	"encoding/json"
	"os"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

func main() {
	m := sdk.Manifest{
		ID:      "fixture",
		Version: "1.0.0",
		Operations: []sdk.Operation{
			{ID: "fixture.echo", Description: "Echo with progress"},
			{ID: "fixture.watch", Description: "Watch poll", ReadOnly: true},
			{ID: "fixture.sleep", Description: "Sleep until cancelled"},
		},
	}
	err := sdk.Serve(m, map[string]sdk.Handler{
		"fixture.echo": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			ic.Log("diag: starting echo")
			ic.Progress(0.5, "halfway")
			var in struct{ Text string ` + "`json:\"text\"`" + ` }
			_ = json.Unmarshal(ic.Input, &in)
			return map[string]string{"echo": in.Text}, nil
		},
		"fixture.watch": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			return &sdk.Observation{
				Done:   ic.Cursor == "cur-2",
				Cursor: "cur-2",
				Data:   json.RawMessage(` + "`" + `{"count":3}` + "`" + `),
			}, nil
		},
		"fixture.sleep": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "fixture-plugin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture plugin: %v\n%s", err, string(out))
	}
	return bin
}

func TestPluginDescribeInvokeWatchCancelAndMalformed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bin := buildFixturePlugin(t)

	// 1. Describe negotiates protocol version and returns 3 operations.
	m, err := plugin.Describe(ctx, bin)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if m.Protocol != sdk.ProtocolVersion || len(m.Operations) != 3 {
		t.Fatalf("unexpected manifest: %+v", m)
	}

	// 2. Invoke echo: captures progress, typed output, and separate stderr diagnostics.
	var gotProgress bool
	res, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary:    bin,
		Operation: "fixture.echo",
		AttemptID: "att-1",
		RequestID: "r1",
		Input:     map[string]string{"text": "hello"},
		OnProgress: func(pct float64, msg string) {
			if pct == 0.5 && msg == "halfway" {
				gotProgress = true
			}
		},
	})
	if err != nil {
		t.Fatalf("Invoke echo: %v", err)
	}
	if !gotProgress || !strings.Contains(string(res.Output), `"hello"`) || !strings.Contains(res.Diagnostics, "diag: starting echo") {
		t.Fatalf("unexpected echo result: progress=%v res=%+v", gotProgress, res)
	}

	// 3. Watch poll returns typed observation + cursor and exits cleanly.
	watchRes, err := plugin.Invoke(ctx, plugin.InvokeConfig{
		Binary:    bin,
		Operation: "fixture.watch",
		AttemptID: "att-1",
		RequestID: "r2",
		Cursor:    "cur-1",
	})
	if err != nil {
		t.Fatalf("Invoke watch: %v", err)
	}
	if watchRes.Observation == nil || watchRes.Observation.Done || watchRes.Observation.Cursor != "cur-2" {
		t.Fatalf("unexpected watch observation: %+v", watchRes.Observation)
	}

	// 4. Cancel stops the child process group.
	cancelCtx, cancelChild := context.WithCancel(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelChild()
	}()
	if res, err := plugin.Invoke(cancelCtx, plugin.InvokeConfig{
		Binary:    bin,
		Operation: "fixture.sleep",
		AttemptID: "att-1",
		RequestID: "r3",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v (ctxErr=%v diag=%q)", err, cancelCtx.Err(), res.Diagnostics)
	}

	// 5. Reject malformed and oversized frames.
	badBin := filepath.Join(t.TempDir(), "bad.sh")
	_ = os.WriteFile(badBin, []byte("#!/bin/sh\necho 'not-json'\n"), 0700)
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{Binary: badBin, Operation: "x"}); !errors.Is(err, plugin.ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}

	bigBin := filepath.Join(t.TempDir(), "big.sh")
	_ = os.WriteFile(bigBin, []byte("#!/bin/sh\npython3 -c 'print(\"x\" * 1200000)'\n"), 0700)
	if _, err := plugin.Invoke(ctx, plugin.InvokeConfig{Binary: bigBin, Operation: "x"}); !errors.Is(err, plugin.ErrOversizedFrame) {
		t.Fatalf("expected ErrOversizedFrame, got %v", err)
	}
}
