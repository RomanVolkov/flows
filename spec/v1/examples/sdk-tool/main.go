package main

import (
	"context"
	"encoding/json"
	"os"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

func main() {
	m := sdk.Manifest{
		ID:      "example-sdk",
		Version: "1.0.0",
		Owner:   "flows",
		Operations: []sdk.Operation{
			{ID: "example-sdk.greet", Description: "Return a greeting", ReadOnly: true},
		},
	}
	err := sdk.Serve(m, map[string]sdk.Handler{
		"example-sdk.greet": func(ctx context.Context, ic sdk.InvokeContext) (any, error) {
			var in struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(ic.Input, &in)
			if in.Name == "" {
				in.Name = "world"
			}
			return map[string]string{"greeting": "hello, " + in.Name}, nil
		},
	})
	if err != nil {
		os.Exit(1)
	}
}
