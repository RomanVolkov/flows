package bridge

import (
	"bufio"
	"encoding/json"
	"io"

	"context"
	"time"

	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ServeMCP runs a JSON-RPC 2.0 MCP stdio bridge backed by svc for one attempt.
func ServeMCP(ctx context.Context, in io.Reader, out io.Writer, svc *Service, token string, outbox *host.Outbox) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)

	for sc.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "flows-bridge", "version": "1.0.0"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			resp.Result = map[string]any{
				"tools": []map[string]string{
					{"name": "catalog_search", "description": "Search catalog operations"},
					{"name": "catalog_describe", "description": "Describe a catalog operation"},
					{"name": "graph_inspect", "description": "Inspect current run graph"},
					{"name": "graph_edit", "description": "Edit current run graph"},
					{"name": "invoke_operation", "description": "Invoke a catalog operation directly"},
					{"name": "report_result", "description": "Report typed attempt completion"},
					{"name": "ask_question", "description": "Ask user a question and notify"},
				},
			}
		case "tools/call":
			var call struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &call); err != nil {
				resp.Error = &struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}{Code: -32602, Message: err.Error()}
				_ = enc.Encode(resp)
				continue
			}
			res, callErr := dispatchMCPTool(ctx, svc, token, outbox, call.Name, call.Arguments)
			if callErr != nil {
				resp.Error = &struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}{Code: -32000, Message: callErr.Error()}
			} else {
				resp.Result = res
			}
		default:
			resp.Error = &struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}{Code: -32601, Message: "method not found"}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func dispatchMCPTool(ctx context.Context, svc *Service, token string, outbox *host.Outbox, name string, args json.RawMessage) (any, error) {
	switch name {
	case "catalog_search":
		var a struct{ Query string `json:"query"` }
		_ = json.Unmarshal(args, &a)
		return svc.CatalogSearch(token, a.Query)
	case "catalog_describe":
		var a struct{ ID string `json:"id"` }
		_ = json.Unmarshal(args, &a)
		op, pkg, err := svc.CatalogDescribe(token, a.ID)
		return map[string]any{"operation": op, "package": pkg}, err
	case "graph_inspect":
		var a struct{ RunID string `json:"run_id"` }
		_ = json.Unmarshal(args, &a)
		return svc.GraphInspect(token, a.RunID)
	case "graph_edit":
		var cmd engine.GraphEditCommand
		if err := json.Unmarshal(args, &cmd); err != nil {
			return nil, err
		}
		return svc.GraphEdit(token, cmd)
	case "invoke_operation":
		var a struct {
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return svc.InvokeDirect(ctx, token, a.Operation, a.Input)
	case "report_result":
		var a struct {
			EventID string          `json:"event_id"`
			Status  store.Status    `json:"status"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Status == "" {
			a.Status = store.Succeeded
		}
		return svc.SubmitResult(ctx, token, a.EventID, a.Status, a.Payload, outbox)
	case "ask_question":
		scope, err := svc.Authorize(token, "question", time.Now())
		if err != nil {
			return nil, err
		}
		var a struct {
			Prompt  string   `json:"prompt"`
			Options []string `json:"options"`
		}
		_ = json.Unmarshal(args, &a)
		q, n, err := svc.Notifications.AskAndNotify(scope.RunID, scope.NodeID, scope.AttemptID, a.Prompt, a.Options)
		return map[string]any{"question": q, "notification": n}, err
	default:
		return nil, ErrCapabilityDenied
	}
}

// HarnessTransportConfig describes the bridge CLI/MCP configuration injected into a harness launch.
type HarnessTransportConfig struct {
	Kind      harness.Kind      `json:"kind"`
	MCPServer map[string]any    `json:"mcp_server"`
	Env       map[string]string `json:"env"`
}

// ConfigureHarnessTransport builds the sandbox-compatible bridge config for Claude, Codex, or agy.
func ConfigureHarnessTransport(kind harness.Kind, scope AttemptScope, bridgeBin string) HarnessTransportConfig {
	return HarnessTransportConfig{
		Kind: kind,
		MCPServer: map[string]any{
			"command": bridgeBin,
			"args":    []string{"bridge", "mcp", "--attempt", scope.AttemptID},
			"env": map[string]string{
				"FLOWS_ATTEMPT_TOKEN": scope.Token,
				"FLOWS_RUN_ID":        scope.RunID,
			},
		},
		Env: map[string]string{
			"FLOWS_ATTEMPT_TOKEN": scope.Token,
			"FLOWS_ATTEMPT_ID":    scope.AttemptID,
			"FLOWS_RUN_ID":        scope.RunID,
		},
	}
}
