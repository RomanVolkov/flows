package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/command"
	"github.com/romanvolkov/flows/internal/paths"
)

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) == 1 {
		switch args[0] {
		case "status", "shutdown":
			if err := service(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		case "--help", "-h":
			fmt.Fprintln(os.Stdout, "Usage: flows {version|status|shutdown}")
			return 0
		}
	}
	return command.Run("flows", args, os.Stdout, os.Stderr)
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
