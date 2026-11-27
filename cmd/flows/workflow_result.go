package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/localapi"
)

func runWorkflowResult(path string) int {
	if err := submitWorkflowResult(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Workflow result saved.")
	return 0
}

func submitWorkflowResult(path string) error {
	socket, attempt, token := os.Getenv("FLOWS_WORKFLOW_SOCKET"), os.Getenv("FLOWS_ATTEMPT_ID"), os.Getenv("FLOWS_ATTEMPT_TOKEN")
	if socket == "" || attempt == "" || token == "" {
		return fmt.Errorf("workflow result submission requires an active workflow harness")
	}
	file := os.Stdin
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
	}
	var output json.RawMessage
	if err := json.NewDecoder(io.LimitReader(file, localapi.MaxBodyBytes)).Decode(&output); err != nil {
		return fmt.Errorf("read result JSON: %w", err)
	}
	body, err := json.Marshal(map[string]any{"attempt_id": attempt, "token": token, "output": output})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api := client.New(socket)
	defer api.Close()
	return api.JSON(ctx, http.MethodPost, "/v1/workflows/result", string(body), &map[string]string{})
}
