package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/cli"
	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/library"
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
	if len(args) >= 2 && args[0] == "catalog" {
		return runCatalog(args[1:])
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
			fmt.Fprintln(os.Stdout, "Usage: flows {version|status|shutdown|validate DIRECTORY [--json]|catalog list|describe ID|spec show TOPIC}")
			return 0
		}
	}
	return command.Run("flows", args, os.Stdout, os.Stderr)
}

func runCatalog(args []string) int {
	home, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	reg := catalog.NewRegistry()
	if err := reg.RegisterBuiltins(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if snap, err := library.Scan(home, nil); err == nil {
		_ = library.PublishTools(snap, reg, nil)
	}
	jsonOut := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
		}
	}
	switch args[0] {
	case "list":
		result := reg.Search("", true)
		if jsonOut {
			if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
		for _, op := range result.Operations {
			fmt.Fprintf(os.Stdout, "%-32s %s\n", op.ID, op.Description)
		}
		return 0
	case "describe":
		if len(args) < 2 || args[1] == "--json" {
			fmt.Fprintln(os.Stderr, "Usage: flows catalog describe OPERATION_ID [--json]")
			return 2
		}
		op, pkg, err := reg.Describe(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		result := struct {
			Operation catalog.OperationDescriptor `json:"operation"`
			Package   catalog.PackageProvenance   `json:"package"`
		}{Operation: op, Package: pkg}
		if jsonOut {
			if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		} else {
			fmt.Fprintf(os.Stdout, "%s\n%s\npackage: %s %s\n", op.ID, op.Description, pkg.PackageID, pkg.Version)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "Usage: flows catalog {list [--json]|describe OPERATION_ID [--json]}")
		return 2
	}
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
	home, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	rep := library.Validate(home, dir)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else if rep.Valid {
		fmt.Fprintf(os.Stdout, "valid %s: %s\n", rep.Kind, rep.ID)
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
