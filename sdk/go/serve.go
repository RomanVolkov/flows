package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// InvokeContext provides request inputs, progress reporting, diagnostics, and callbacks.
type InvokeContext struct {
	RequestID string
	AttemptID string
	RunID     string
	Operation string
	Cursor    string
	Input     json.RawMessage
	Client    *CallbackClient

	send   func(Frame)
	stderr io.Writer
}

// Progress emits a typed progress frame to the engine.
func (c InvokeContext) Progress(percent float64, message string) {
	c.send(Frame{
		Protocol:  ProtocolVersion,
		RequestID: c.RequestID,
		Kind:      "progress",
		Percent:   percent,
		Message:   message,
	})
}

// Milestone publishes a named intermediate outcome before the operation returns.
func (c InvokeContext) Milestone(name string) {
	c.send(Frame{Protocol: ProtocolVersion, RequestID: c.RequestID, Kind: "milestone", Message: name})
}

// Log writes diagnostic text to stderr, separate from typed stdout frames.
func (c InvokeContext) Log(format string, args ...any) {
	if c.stderr != nil {
		_, _ = fmt.Fprintf(c.stderr, format+"\n", args...)
	}
}

// Handler executes a single plugin operation. Returning *Observation emits a
// watch observation frame; any other value emits a typed result frame.
type Handler func(ctx context.Context, ic InvokeContext) (any, error)

// Serve runs the stdio frame loop for manifest and handlers.
func Serve(manifest Manifest, handlers map[string]Handler) error {
	return ServeStreams(os.Stdin, os.Stdout, os.Stderr, manifest, handlers)
}

// ServeStreams runs the versioned NDJSON protocol over arbitrary streams.
func ServeStreams(in io.Reader, out, errOut io.Writer, manifest Manifest, handlers map[string]Handler) error {
	manifest.Protocol = ProtocolVersion
	var writeMu sync.Mutex
	send := func(f Frame) {
		f.Protocol = ProtocolVersion
		data, err := json.Marshal(f)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = out.Write(append(data, '\n'))
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes)

	var cancelMu sync.Mutex
	cancels := make(map[string]context.CancelFunc)
	var wg sync.WaitGroup

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			send(Frame{
				Kind:  "error",
				Error: &FrameError{Code: "malformed_request", Message: err.Error()},
			})
			continue
		}
		if req.Protocol != ProtocolVersion {
			send(Frame{
				RequestID: req.RequestID,
				Kind:      "error",
				Error:     &FrameError{Code: "incompatible_protocol", Message: fmt.Sprintf("unsupported protocol %d", req.Protocol)},
			})
			continue
		}

		switch req.Kind {
		case "describe":
			m := manifest
			send(Frame{RequestID: req.RequestID, Kind: "manifest", Manifest: &m})

		case "cancel":
			cancelMu.Lock()
			if fn, ok := cancels[req.RequestID]; ok {
				fn()
			}
			cancelMu.Unlock()

		case "invoke":
			h, ok := handlers[req.Operation]
			if !ok {
				send(Frame{
					RequestID: req.RequestID,
					Kind:      "error",
					Error:     &FrameError{Code: "unknown_operation", Message: fmt.Sprintf("unknown operation %q", req.Operation)},
				})
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancelMu.Lock()
			cancels[req.RequestID] = cancel
			cancelMu.Unlock()

			var cb *CallbackClient
			if req.CallbackSocket != "" {
				cb = NewCallbackClient(req.CallbackSocket, req.AttemptID, req.RunID, req.CallbackToken)
			}

			wg.Add(1)
			go func(r Request) {
				defer wg.Done()
				defer func() {
					cancelMu.Lock()
					delete(cancels, r.RequestID)
					cancelMu.Unlock()
					cancel()
				}()

				ic := InvokeContext{
					RequestID: r.RequestID,
					AttemptID: r.AttemptID,
					RunID:     r.RunID,
					Operation: r.Operation,
					Cursor:    r.Cursor,
					Input:     r.Input,
					Client:    cb,
					send:      send,
					stderr:    errOut,
				}
				val, err := h(ctx, ic)
				if err != nil {
					send(Frame{
						RequestID: r.RequestID,
						Kind:      "error",
						Error:     &FrameError{Code: "execution_failed", Message: err.Error()},
					})
					return
				}
				if obs, ok := val.(*Observation); ok {
					send(Frame{RequestID: r.RequestID, Kind: "observation", Observation: obs})
					return
				}
				if obs, ok := val.(Observation); ok {
					send(Frame{RequestID: r.RequestID, Kind: "observation", Observation: &obs})
					return
				}
				rawOut, err := json.Marshal(val)
				if err != nil {
					send(Frame{
						RequestID: r.RequestID,
						Kind:      "error",
						Error:     &FrameError{Code: "marshal_error", Message: err.Error()},
					})
					return
				}
				send(Frame{RequestID: r.RequestID, Kind: "result", Output: rawOut})
			}(req)
		}
	}
	wg.Wait()
	return scanner.Err()
}
