package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
)

// Set FLOWS_TEST_PACKAGED_DAEMON to exercise the installed interactive programs.
// The test types a character without submitting a prompt or invoking a model.
func TestPackagedHarnessTerminal(t *testing.T) {
	binary := os.Getenv("FLOWS_TEST_PACKAGED_DAEMON")
	if binary == "" {
		t.Skip("requires the packaged daemon, codex and agy")
	}
	homeDir := t.TempDir()
	t.Setenv("FLOWS_HOME", filepath.Join(homeDir, "flows"))
	t.Setenv("HOME", homeDir)
	t.Setenv("CODEX_HOME", filepath.Join(homeDir, "codex"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	daemon := exec.CommandContext(ctx, binary, "run")
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	c := client.New(home.Socket())
	defer c.Close()
	defer func() { _ = daemon.Process.Signal(os.Interrupt); _ = daemon.Wait() }()
	for {
		if _, err := c.Version(ctx); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, agent := range []string{"agy", "codex"} {
		t.Run(agent, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"request_id": "test-" + agent, "harness": agent})
			var run localapi.CreateRunResponse
			if err := c.JSON(ctx, "POST", "/v1/runs", string(body), &run); err != nil {
				t.Fatal(err)
			}
			defer func() { var result any; _ = c.JSON(ctx, "POST", "/v1/sessions/"+run.SessionID+"/stop", "", &result) }()
			var output string
			until := time.Now().Add(10 * time.Second)
			for time.Now().Before(until) {
				var preview struct{ Screen string }
				if err := c.JSON(ctx, "GET", "/v1/preview/"+run.AttemptID, "", &preview); err != nil {
					t.Fatal(err)
				}
				decoded, _ := base64.StdEncoding.DecodeString(preview.Screen)
				output = strings.ToLower(string(decoded))
				if strings.Contains(output, agent) || strings.Contains(output, "antigravity") || strings.Contains(output, "sign in") {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			if !strings.Contains(output, agent) && !strings.Contains(output, "antigravity") && !strings.Contains(output, "sign in") {
				t.Fatalf("missing harness startup screen (%d bytes)", len(output))
			}
			term, err := host.DialAttach(ctx, run.SocketPath, run.SessionID, host.AttachOptions{ClientID: "smoke"})
			if err != nil {
				t.Fatal(err)
			}
			defer term.Close()
			wait, stop := context.WithTimeout(ctx, 8*time.Second)
			defer stop()
			for {
				frame, err := term.ReadFrame(wait)
				if err != nil {
					t.Fatal(err)
				}
				if frame.Kind == host.FrameExit {
					t.Fatal("harness exited during startup")
				}
				if frame.Kind == host.FrameInputOwner {
					break
				}
			}
			if err := term.WriteInput(wait, []byte("?")); err != nil {
				t.Fatal(err)
			}
			if err := term.SendResize(wait, 100, 30, 0, 0); err != nil {
				t.Fatal(err)
			}
			for {
				frame, err := term.ReadFrame(wait)
				if err != nil {
					t.Fatal(err)
				}
				if frame.Kind == host.FrameExit {
					t.Fatal("harness exited on input")
				}
				if frame.Kind == host.FrameOutput {
					break
				}
			}
		})
	}
}
