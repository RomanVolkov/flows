package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/install"
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
		case "register":
			if err := register(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
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
			fmt.Fprintln(os.Stdout, "Usage: flowsd {version|register|run [--home <dir>]}")
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

func register() error {
	home, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := home.Init(); err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	env := home.Environment()
	label := home.ServiceLabel()
	plist := filepath.Join(env["HOME"], "Library", "LaunchAgents", label+".plist")
	return install.RegisterLaunchd(context.Background(), nil, plist, install.LaunchdConfig{
		Label: label, DaemonBin: bin, HomeDir: env["FLOWS_HOME"], LogDir: home.Logs, ConfigHome: env["XDG_CONFIG_HOME"],
		SearchPath: strings.Join([]string{filepath.Join(env["HOME"], ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":"),
	})
}
