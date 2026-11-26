package bridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/activity"
	"github.com/romanvolkov/flows/internal/bridge"
	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

func TestBridgeValidationOfflineCorrectionOutboxReplayAndMCP(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()

	st, err := store.Open(ctx, filepath.Join(stateDir, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runID, _ := st.CreateRun(ctx, nil)
	node, _ := st.AddNode(ctx, store.NewNode{RunID: runID, Kind: "session", Name: "review"})
	att, _ := st.StartAttempt(ctx, node.ID)

	cat := catalog.NewRegistry()
	_ = cat.RegisterBuiltins()
	_ = cat.RegisterPackage(catalog.PackageProvenance{
		PackageID: "fixture",
		Namespace: "fixture",
		Version:   "1.0.0",
		Origin:    "user",
		Enabled:   true,
		Operations: []catalog.OperationDescriptor{
			{ID: "fixture.ping", Description: "ping", ExecutionKind: catalog.ExecPlugin},
		},
	})

	gm := engine.NewGraphManager()
	gm.InitRun(runID, []engine.LogicalStep{
		{ID: node.ID, RunID: runID, Kind: "session", Name: "review", Status: engine.StepReady},
	}, nil)

	actLog, err := activity.NewLog(filepath.Join(stateDir, "activities"))
	if err != nil {
		t.Fatal(err)
	}
	nc, err := builtin.NewNotificationCenter(stateDir)
	if err != nil {
		t.Fatal(err)
	}

	svc := bridge.NewService(cat, gm, st, actLog, nc, func(ctx context.Context, op catalog.OperationDescriptor, input json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"pong":true}`), nil
	})

	pinnedSchema := json.RawMessage(`{
		"type":"object",
		"required":["approved","findings"],
		"properties":{
			"approved":{"type":"boolean"},
			"findings":{"type":"array","items":{"type":"string"}}
		}
	}`)
	scope := bridge.AttemptScope{
		RunID:        runID,
		NodeID:       node.ID,
		AttemptID:    att.ID,
		Token:        "tok-1",
		ExpiresAt:    time.Now().Add(time.Hour).UnixNano(),
		Capabilities: []string{"catalog", "graph_read", "graph_edit", "invoke", "complete", "question"},
		PinnedSchema: pinnedSchema,
	}
	svc.RegisterScope(scope)

	ob, err := host.NewOutbox(stateDir, att.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Missing approved -> field diagnostic BEFORE outbox capture.
	_, err = svc.SubmitResult(ctx, "tok-1", "evt-bad-1", store.Succeeded, json.RawMessage(`{"findings":["ok"]}`), ob)
	var valErr *bridge.ResultValidationError
	if !errors.As(err, &valErr) || valErr.Errors[0].Field != "approved" {
		t.Fatalf("expected missing approved diagnostic, got %v", err)
	}
	// Wrong findings type -> field diagnostic BEFORE outbox capture.
	_, err = svc.SubmitResult(ctx, "tok-1", "evt-bad-2", store.Succeeded, json.RawMessage(`{"approved":true,"findings":"not-array"}`), ob)
	if !errors.As(err, &valErr) || valErr.Errors[0].Field != "findings" {
		t.Fatalf("expected wrong findings type diagnostic, got %v", err)
	}
	if pend, _ := ob.Pending(); len(pend) != 0 {
		t.Fatalf("invalid results must not be captured in outbox")
	}

	// 2. Same-turn correction while offline (store=nil): captured locally in outbox.
	offlineSvc := bridge.NewService(cat, gm, nil, actLog, nc, nil)
	offlineSvc.RegisterScope(scope)
	csOffline, err := offlineSvc.SubmitResult(ctx, "tok-1", "evt-ok-1", store.Succeeded, json.RawMessage(`{"approved":true,"findings":["clean"]}`), ob)
	if err != nil || csOffline.State != "saved_locally" {
		t.Fatalf("expected saved_locally while offline, got %+v err=%v", csOffline, err)
	}

	// 3. Replay captured outbox record to daemon store (even after source schema changed, using pinnedSchema).
	pending, _ := ob.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending outbox record, got %d", len(pending))
	}
	csAccepted, err := bridge.DeliverOutboxRecord(ctx, st, pending[0].AttemptID, pending[0].EventID, store.Status(pending[0].Status), pending[0].Payload, ob, pinnedSchema)
	if err != nil || csAccepted.State != "accepted" {
		t.Fatalf("expected accepted on replay, got %+v err=%v", csAccepted, err)
	}

	// Duplicate delivery with conflicting payload -> ErrCompletionConflict.
	if _, err := bridge.DeliverOutboxRecord(ctx, st, att.ID, "evt-ok-1", store.Succeeded, json.RawMessage(`{"approved":false,"findings":[]}`), nil, pinnedSchema); !errors.Is(err, store.ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}

	// 4. Superseded/closed attempt -> rejected disposition.
	csRej, err := bridge.DeliverOutboxRecord(ctx, st, att.ID, "evt-late", store.Succeeded, json.RawMessage(`{"approved":true,"findings":[]}`), nil, pinnedSchema)
	if err != nil || csRej.State != "rejected" {
		t.Fatalf("expected rejected for closed attempt, got %+v err=%v", csRej, err)
	}

	// 5. MCP stdio round-trip: catalog_search, invoke_operation (records ActivityRecord), graph_edit follow-up, ask_question.
	mcpIn := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"catalog_search","arguments":{"query":"fixture"}}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invoke_operation","arguments":{"operation":"fixture.ping","input":{}}}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"graph_edit","arguments":{"expected_revision":1,"idempotency_key":"k-fup","operations":[{"op":"add_node","node":{"id":"followup","kind":"tool","name":"followup"}}]}}}` + "\n" +
			`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ask_question","arguments":{"prompt":"continue?"}}}` + "\n",
	)
	var mcpOut bytes.Buffer
	if err := bridge.ServeMCP(ctx, mcpIn, &mcpOut, svc, "tok-1", ob); err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}
	if !strings.Contains(mcpOut.String(), `"pong":true`) || !strings.Contains(mcpOut.String(), `"new_revision":2`) {
		t.Fatalf("unexpected MCP output: %s", mcpOut.String())
	}

	acts, err := actLog.List(att.ID)
	if err != nil || len(acts) != 1 || acts[0].Mode != "direct" || acts[0].OperationID != "fixture.ping" {
		t.Fatalf("expected recorded direct activity, got %+v err=%v", acts, err)
	}

	// 6. User-selected timestamped terminal excerpt + expired credential check + transport config for all 3 harnesses.
	if err := actLog.RecordExcerpt(activity.TerminalExcerpt{
		AttemptID: att.ID, StartSeq: 10, EndSeq: 45, Text: "user: fix lint\nassistant: fixed", CoverageGap: "unstructured_tui",
	}); err != nil {
		t.Fatal(err)
	}
	excerpts, _ := actLog.ListExcerpts(att.ID)
	if len(excerpts) != 1 || excerpts[0].CoverageGap != "unstructured_tui" {
		t.Fatalf("unexpected excerpts: %+v", excerpts)
	}

	for _, k := range []harness.Kind{harness.KindClaude, harness.KindCodex, harness.KindAgy} {
		cfg := bridge.ConfigureHarnessTransport(k, scope, "flows")
		if cfg.Env["FLOWS_ATTEMPT_TOKEN"] != "tok-1" {
			t.Fatalf("missing token for %s", k)
		}
	}

	svc.RevokeScope("tok-1")
	if _, err := svc.Authorize("tok-1", "catalog", time.Now()); !errors.Is(err, bridge.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken after revoke, got %v", err)
	}
}
