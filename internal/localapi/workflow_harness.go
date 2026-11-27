package localapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/romanvolkov/flows/desktop/theme"
	"github.com/romanvolkov/flows/internal/bridge"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

type harnessResultScope struct {
	token    string
	schema   json.RawMessage
	outbox   *host.Outbox
	result   chan json.RawMessage
	accepted json.RawMessage
}

func (r *workflowRunner) submitHarnessResult(w http.ResponseWriter, req *http.Request) {
	if !method(w, req, http.MethodPost) {
		return
	}
	var result struct {
		AttemptID string          `json:"attempt_id"`
		Token     string          `json:"token"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, MaxBodyBytes)).Decode(&result); err != nil {
		writeError(w, 400, "invalid_result", "expected an attempt ID and JSON result")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	scope := r.results[result.AttemptID]
	if scope == nil || subtle.ConstantTimeCompare([]byte(scope.token), []byte(result.Token)) != 1 {
		writeError(w, 403, "invalid_attempt", "result is outside the active attempt")
		return
	}
	var output map[string]any
	if err := json.Unmarshal(result.Output, &output); err != nil || output == nil {
		writeError(w, 422, "invalid_result", "expected a JSON object")
		return
	}
	if len(scope.schema) > 0 {
		if err := bridge.ValidateResultPayload(scope.schema, result.Output); err != nil {
			writeError(w, 422, "invalid_result", err.Error())
			return
		}
	}
	normalized, _ := json.Marshal(output)
	if len(scope.accepted) > 0 && string(scope.accepted) != string(normalized) {
		writeError(w, 409, "result_already_submitted", "the attempt has already submitted a result")
		return
	}
	if len(scope.accepted) == 0 {
		if _, err := scope.outbox.Capture("workflow-result", string(store.Succeeded), normalized, nil); err != nil {
			writeError(w, 500, "result_save_failed", err.Error())
			return
		}
		scope.accepted = normalized
		scope.result <- normalized
	}
	writeJSON(w, 200, map[string]string{"status": "saved"})
}

func harnessResultSchema(port workflow.PortSpec) (json.RawMessage, error) {
	if len(port.ResolvedSchema) > 0 {
		return port.ResolvedSchema, nil
	}
	if len(port.Fields) == 0 {
		return nil, nil
	}
	var required []string
	for key, field := range port.Fields {
		if field.Required {
			required = append(required, key)
		}
	}
	return json.Marshal(map[string]any{"type": "object", "properties": port.Fields, "required": required})
}

func (r *workflowRunner) executeHarness(ctx context.Context, spec store.NewWorkflowRun, step store.WorkflowStep,
	attempt store.Attempt, snapshot *workflow.LaunchSnapshot, with map[string]any) (map[string]any, error) {
	settings, err := config.LoadSettings(r.library.home.Settings())
	if err != nil {
		return nil, err
	}
	kind, _ := with["harness"].(string)
	if kind == "" {
		kind = string(settings.DefaultHarness)
	}
	adapter, err := harness.ForKind(harness.Kind(kind))
	if err != nil {
		return nil, err
	}
	profile := config.ProfileFromSettings(adapter.Kind(), settings)
	caps, err := adapter.Probe(ctx, profile.Executable)
	if err != nil {
		return nil, err
	}
	prompt, _ := with["prompt"].(string)
	if name, _ := with["prompt_template"].(string); name != "" {
		template, ok := snapshot.Templates[filepath.Clean(name)]
		if !ok {
			return nil, fmt.Errorf("prompt template %q was not pinned", name)
		}
		args, _ := with["arguments"].(map[string]any)
		rendered, err := workflow.RenderPrompt(name, template, args, nil)
		if err != nil {
			return nil, err
		}
		prompt = rendered.Text
	}
	port := snapshot.Compiled.Ports[step.StepID]
	var scope *harnessResultScope
	callbackEnv := map[string]string{}
	if !port.CompletionOnly && (len(port.Fields) > 0 || len(port.ResolvedSchema) > 0) {
		schema, err := harnessResultSchema(port)
		if err != nil {
			return nil, err
		}
		outbox, err := host.NewOutbox(r.library.home.State, attempt.ID)
		if err != nil {
			return nil, err
		}
		scope = &harnessResultScope{token: rand.Text(), schema: schema, outbox: outbox, result: make(chan json.RawMessage, 1)}
		callbackEnv["FLOWS_ATTEMPT_ID"] = attempt.ID
		callbackEnv["FLOWS_ATTEMPT_TOKEN"] = scope.token
		callbackEnv["FLOWS_WORKFLOW_SOCKET"] = r.library.home.Socket()
		prompt += "\n\nSubmit the final result as a JSON object using `flows bridge result <file>` or pipe it to `flows bridge result -`. The result schema is:\n" + string(schema)
		r.mu.Lock()
		if r.results == nil {
			r.results = make(map[string]*harnessResultScope)
		}
		r.results[attempt.ID] = scope
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			delete(r.results, attempt.ID)
			r.mu.Unlock()
		}()
	}

	dir := spec.WorkDir
	if dir == "" {
		dir, _ = with["working_dir"].(string)
	}
	if dir == "" {
		base := filepath.Join(r.library.home.State, "chats")
		if err := os.MkdirAll(base, 0700); err != nil {
			return nil, err
		}
		dir, err = os.MkdirTemp(base, "workflow-")
		if err != nil {
			return nil, err
		}
	}
	launch, err := adapter.BuildLaunch(caps, harness.LaunchSpec{Profile: profile, Prompt: prompt, WorkDir: dir, CallbackEnv: callbackEnv})
	if err != nil {
		return nil, err
	}
	h, err := r.runs.startWorkflowHost(ctx, attempt.ID, launch)
	if err != nil {
		return nil, err
	}
	exit := make(chan host.Record, 1)
	go func() { exit <- h.Wait() }()
	defer func() {
		_ = h.RequestStop(800 * time.Millisecond)
		r.runs.sessions.RecordExit(h.Wait())
	}()
	var result <-chan json.RawMessage
	if scope != nil {
		result = scope.result
	}
	readResult := func(raw json.RawMessage) (map[string]any, error) {
		var outputs map[string]any
		err := json.Unmarshal(raw, &outputs)
		return outputs, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case output := <-result:
		return readResult(output)
	case rec := <-exit:
		if rec.ExitCode == nil || *rec.ExitCode != 0 {
			return nil, fmt.Errorf("harness exited unsuccessfully (signal %s)", rec.ExitSignal)
		}
		if scope == nil {
			return map[string]any{}, nil
		}
		select {
		case output := <-result:
			return readResult(output)
		default:
			return nil, fmt.Errorf("harness exited without submitting its required outputs")
		}
	}
}

// The workflow runner owns completion of these hosts because their output must
// commit with the step outcome before a downstream node can start.
func (m *RunManager) startWorkflowHost(ctx context.Context, attemptID string, launch harness.ResolvedLaunch) (*host.Host, error) {
	id := "sess-" + shortHex(8)
	h, err := host.Start(ctx, host.Config{
		SessionID: id, AttemptID: attemptID, StateDir: filepath.Join(m.home.State, "hosts", id),
		SocketPath: filepath.Join(m.home.Runtime, "h-"+id+".sock"), WorkDir: launch.WorkDir,
		Command: launch.Argv[0], Args: launch.Argv[1:], Env: launch.Env, InitialPrompt: launch.PTYPrompt,
		InitialScreen: []byte(theme.TerminalOSC()),
	})
	if err != nil {
		return nil, err
	}
	if err := h.WaitCheckoutReady(ctx); err != nil {
		_ = h.RequestStop(800 * time.Millisecond)
		_ = h.Close()
		return nil, err
	}
	m.mu.Lock()
	m.hosts[attemptID] = h
	m.mu.Unlock()
	rec := h.Record()
	m.sessions.mu.Lock()
	m.sessions.adopted[id] = rec
	m.sessions.leases[attemptID] = host.LeaseHeld
	m.sessions.mu.Unlock()
	return h, nil
}
