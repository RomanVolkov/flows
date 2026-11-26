package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
)

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) >= 1 {
		switch args[0] {
		case "run", "--foreground":
			if len(args) == 3 && args[1] == "--home" && args[2] != "" {
				_ = os.Setenv("FLOWS_HOME", args[2])
			}
			if err := serve(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		case "--home":
			if len(args) == 2 && args[1] != "" {
				_ = os.Setenv("FLOWS_HOME", args[1])
				if err := serve(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				return 0
			}
		case "--help", "-h":
			fmt.Fprintln(os.Stdout, "Usage: flowsd {version|run [--home <dir>]}")
			return 0
		}
	}
	return command.Run("flowsd", args, os.Stdout, os.Stderr)
}

func serve() error {
	home, err := paths.Resolve()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return localapi.Run(ctx, home, command.Version)
}
