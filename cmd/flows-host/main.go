package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/host"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			if err := runServe(args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		case "attach":
			if err := runAttach(args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
	}
	return command.Run("flows-host", args, os.Stdout, os.Stderr)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID")
	attemptID := fs.String("attempt", "", "attempt ID")
	socketPath := fs.String("socket", "", "unix socket path")
	stateDir := fs.String("state-dir", "", "durable host state directory")
	workDir := fs.String("cwd", "", "working directory")
	prompt := fs.String("prompt", "", "initial prompt")
	cols := fs.Uint("cols", 80, "terminal columns")
	rows := fs.Uint("rows", 24, "terminal rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("serve requires a command after flags")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	h, err := host.Start(ctx, host.Config{
		SessionID:     *sessionID,
		AttemptID:     *attemptID,
		StateDir:      *stateDir,
		SocketPath:    *socketPath,
		WorkDir:       *workDir,
		Command:       rest[0],
		Args:          rest[1:],
		InitialPrompt: *prompt,
		Cols:          uint16(*cols),
		Rows:          uint16(*rows),
	})
	if err != nil {
		return err
	}
	defer h.Close()

	rec := h.Wait()
	if rec.ExitCode != nil && *rec.ExitCode != 0 {
		return fmt.Errorf("session exited with code %d", *rec.ExitCode)
	}
	return nil
}

func runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID")
	socketPath := fs.String("socket", "", "unix socket path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *socketPath == "" {
		return fmt.Errorf("attach requires --session and --socket")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if opts := host.NativeAttachOptionsFromEnv(); opts.ControlSocket != "" {
		return host.AttachNative(ctx, *socketPath, *sessionID, os.Stdin, os.Stdout, opts)
	}
	return host.AttachTTY(ctx, *socketPath, *sessionID, os.Stdin, os.Stdout)
}
