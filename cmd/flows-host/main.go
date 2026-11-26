package main

import (
	"os"

	"github.com/romanvolkov/flows/internal/command"
)

func main() {
	os.Exit(command.Run("flows-host", os.Args[1:], os.Stdout, os.Stderr))
}
