package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/cli"
	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/paths"
)

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "validate" {
		return runValidate(args[1:])
	}
	if len(args) >= 2 && args[0] == "spec" {
		return runSpec(args[1:])
	}
	if len(args) == 1 {
		switch args[0] {
		case "status", "shutdown":
			if err := service(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		case "--help", "-h":
			fmt.Fprintln(os.Stdout, "Usage: flows {version|status|shutdown|validate DIRECTORY [--json]}")
			return 0
		}
	}
	return command.Run("flows", args, os.Stdout, os.Stderr)
}

func runValidate(args []string) int {
	dir := ""
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else if dir == "" {
			dir = a
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "Usage: flows validate DIRECTORY [--json]")
		return 2
	}
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()
	rep := cli.ValidateDirectory(dir, reg)
	if jsonOut {
		raw, _ := cli.FormatValidateJSON(rep)
		fmt.Fprintln(os.Stdout, string(raw))
	} else if rep.Valid {
		fmt.Fprintf(os.Stdout, "valid: %s\n", rep.WorkflowID)
	} else {
		for _, d := range rep.Diagnostics {
			fmt.Fprintln(os.Stderr, d.Error())
		}
	}
	if rep.Valid {
		return 0
	}
	return 1
}

func runSpec(args []string) int {
	home, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(os.Stdout, cli.SpecPath(home, cli.SpecVersion))
		return 0
	case "show":
		topic := "authoring"
		if len(args) > 1 {
			topic = args[1]
		}
		data, err := cli.SpecShow(home, topic)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stdout, string(data))
		return 0
	default:
		fmt.Fprintln(os.Stderr, "Usage: flows spec {path|show [authoring|protocol|workflow|tool]}")
		return 2
	}
}

func service(action string) error {
	home, err := paths.Resolve()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var start client.Starter
	if action == "status" {
		start = client.StartInstalled
	}
	c, err := client.Connect(ctx, home, start)
	if err != nil {
		return err
	}
	defer c.Close()
	if action == "shutdown" {
		return c.Shutdown(ctx)
	}
	health, err := c.Health(ctx)
	if err == nil {
		fmt.Fprintln(os.Stdout, health.Status)
	}
	return err
}
