//go:build ghostty

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/romanvolkov/flows/internal/terminal"
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) >= 3 && args[0] == "host" {
		state, err := terminal.NewGhosttyState()
		if err != nil {
			return err
		}
		defer state.Close()
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		cmd := exec.Command(args[2], args[3:]...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		return terminal.Serve(ctx, args[1], cmd, state)
	}
	if len(args) == 2 && args[0] == "attach" {
		return terminal.Attach(args[1], os.Stdin, os.Stdout)
	}
	if len(args) == 2 && args[0] == "window" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		app := filepath.Join(filepath.Dir(exe), "flows-terminal-window")
		relay := shellQuote(exe) + " attach " + shellQuote(args[1])
		return syscall.Exec(app, []string{app, relay}, os.Environ())
	}
	return errors.New("usage: flows-terminal-probe host SOCKET COMMAND [ARGS...] | window SOCKET | attach SOCKET")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
