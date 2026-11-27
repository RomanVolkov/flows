//go:build ghostty

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/terminal"
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(args []string) error {
	if len(args) >= 3 && args[0] == "host" {
		state, err := terminal.NewGhosttyState()
		if err != nil {
			return err
		}
		defer state.Close()

		stateDir := os.Getenv("FLOWS_PROBE_STATE_DIR")
		if stateDir == "" {
			tmpDir, err := os.MkdirTemp("", "flows-probe-host-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmpDir)
			stateDir = tmpDir
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		h, err := host.Start(ctx, host.Config{
			SessionID:  envOr("FLOWS_PROBE_SESSION", "probe"),
			AttemptID:  envOr("FLOWS_PROBE_ATTEMPT", "att-probe"),
			StateDir:   stateDir,
			SocketPath: args[1],
			Command:    args[2],
			Args:       args[3:],
			State:      state,
		})
		if err != nil {
			return err
		}
		defer h.Close()

		if err := h.WaitCheckoutReady(ctx); err != nil {
			return err
		}
		rec := h.Wait()
		if rec.ExitCode != nil && *rec.ExitCode != 0 {
			return fmt.Errorf("probe session exited with code %d", *rec.ExitCode)
		}
		return nil
	}
	if len(args) >= 2 && args[0] == "attach" {
		sessionID := envOr("FLOWS_PROBE_SESSION", "probe")
		if len(args) >= 3 {
			sessionID = args[2]
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return host.AttachNative(ctx, args[1], sessionID, os.Stdin, os.Stdout, host.NativeAttachOptionsFromEnv())
	}
	if len(args) >= 3 && args[0] == "scroll" {
		rows, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid scroll rows %q: %w", args[2], err)
		}
		sessionID := envOr("FLOWS_PROBE_SESSION", "probe")
		if len(args) >= 4 {
			sessionID = args[3]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := host.DialAttach(ctx, args[1], sessionID, host.AttachOptions{ScreenOnly: true})
		if err != nil {
			return err
		}
		defer conn.Close()
		for {
			f, err := conn.ReadFrame(ctx)
			if err != nil {
				return err
			}
			if f.Kind == host.FrameControl {
				var ctrl host.ControlMessage
				if json.Unmarshal(f.Payload, &ctrl) == nil && ctrl.Type == "replay_end" {
					break
				}
			}
		}
		if err := conn.SendControl(ctx, host.ControlMessage{Type: "scroll", Rows: rows}); err != nil {
			return err
		}
		// Wait briefly for the host to apply and flush the updated screen before detaching.
		_, _ = conn.ReadFrame(ctx)
		return nil
	}
	if len(args) >= 2 && args[0] == "window" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		sessionID := envOr("FLOWS_PROBE_SESSION", "probe")
		if len(args) >= 3 {
			sessionID = args[2]
		}
		app := filepath.Join(filepath.Dir(exe), "flows-terminal-window")
		return syscall.Exec(app, []string{app, exe, args[1], sessionID}, os.Environ())
	}
	return errors.New("usage: flows-terminal-probe host SOCKET COMMAND [ARGS...] | window SOCKET [SESSION] | attach SOCKET [SESSION] | scroll SOCKET ROWS [SESSION]")
}
