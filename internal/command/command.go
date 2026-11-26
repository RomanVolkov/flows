// Package command provides flags shared by the Flows executables.
package command

import (
	"fmt"
	"io"
)

// Version can be set by release builds using the linker.
var Version = "dev"

func Run(name string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 {
		switch args[0] {
		case "version", "--version":
			fmt.Fprintf(stdout, "%s %s\n", name, Version)
			return 0
		case "--help", "-h":
			fmt.Fprintf(stdout, "Usage: %s version\n", name)
			return 0
		}
	}
	fmt.Fprintf(stderr, "Usage: %s version\n", name)
	return 2
}
