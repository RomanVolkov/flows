// Package plugin executes versioned plugin executables over stdio NDJSON frames.
package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	sdk "github.com/romanvolkov/flows/sdk/go"
)

const (
	defaultTimeout        = 30 * time.Second
	defaultMaxOutputBytes = 4 * 1024 * 1024
)

var (
	ErrOversizedFrame = errors.New("plugin frame exceeds size limit")
	ErrMalformedFrame = errors.New("malformed plugin frame")
)

// InvokeConfig configures one plugin operation invocation.
type InvokeConfig struct {
	Binary         string
	Operation      string
	AttemptID      string
	RunID          string
	RequestID      string
	Input          any
	Cursor         string
	CallbackSocket string
	CallbackToken  string
	Timeout        time.Duration
	MaxOutputBytes int
	OnProgress     func(percent float64, message string)
}

// InvokeResult holds the typed output, optional watch observation, and stderr diagnostics.
type InvokeResult struct {
	RequestID   string           `json:"request_id"`
	Output      json.RawMessage  `json:"output,omitempty"`
	Observation *sdk.Observation `json:"observation,omitempty"`
	Diagnostics string           `json:"diagnostics,omitempty"`
}

// Describe negotiates protocol version and fetches the plugin's Manifest.
func Describe(ctx context.Context, binary string) (sdk.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return sdk.Manifest{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sdk.Manifest{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return sdk.Manifest{}, err
	}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	}()

	req := sdk.Request{
		Protocol:  sdk.ProtocolVersion,
		RequestID: "describe-1",
		Kind:      "describe",
	}
	raw, _ := json.Marshal(req)
	_, _ = stdin.Write(append(raw, '\n'))
	_ = stdin.Close()

	frame, err := readFrameLine(ctx, stdout, sdk.MaxFrameBytes)
	if err != nil {
		return sdk.Manifest{}, err
	}
	if frame.Protocol != sdk.ProtocolVersion {
		return sdk.Manifest{}, fmt.Errorf("incompatible plugin protocol %d", frame.Protocol)
	}
	if frame.Error != nil {
		return sdk.Manifest{}, frame.Error
	}
	if frame.Kind != "manifest" || frame.Manifest == nil {
		return sdk.Manifest{}, fmt.Errorf("expected manifest frame, got %q", frame.Kind)
	}
	return *frame.Manifest, nil
}

// Invoke executes one plugin operation over stdio, capturing progress, typed
// result/observation, and stderr diagnostics, and killing the process group on cancellation.
func Invoke(ctx context.Context, cfg InvokeConfig) (InvokeResult, error) {
	if cfg.Binary == "" || cfg.Operation == "" {
		return InvokeResult{}, fmt.Errorf("binary and operation are required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxOut := cfg.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = defaultMaxOutputBytes
	}
	reqID := cfg.RequestID
	if reqID == "" {
		reqID = "req-1"
	}
	if cfg.AttemptID != "" {
		reqID = cfg.AttemptID + ":" + reqID
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var inputRaw json.RawMessage
	if cfg.Input != nil {
		b, err := json.Marshal(cfg.Input)
		if err != nil {
			return InvokeResult{}, err
		}
		inputRaw = b
	}

	cmd := exec.Command(cfg.Binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return InvokeResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return InvokeResult{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return InvokeResult{}, err
	}
	pgid := cmd.Process.Pid

	unblock := context.AfterFunc(ctx, func() {
		cancelRaw, _ := json.Marshal(sdk.Request{
			Protocol:  sdk.ProtocolVersion,
			RequestID: reqID,
			Kind:      "cancel",
		})
		_, _ = stdin.Write(append(cancelRaw, '\n'))
		_ = stdin.Close()
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
	defer unblock()

	req := sdk.Request{
		Protocol:       sdk.ProtocolVersion,
		RequestID:      reqID,
		Kind:           "invoke",
		Operation:      cfg.Operation,
		AttemptID:      cfg.AttemptID,
		RunID:          cfg.RunID,
		CallbackSocket: cfg.CallbackSocket,
		CallbackToken:  cfg.CallbackToken,
		Cursor:         cfg.Cursor,
		Input:          inputRaw,
	}
	reqRaw, _ := json.Marshal(req)
	if _, err := stdin.Write(append(reqRaw, '\n')); err != nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		return InvokeResult{}, err
	}
	defer stdin.Close()

	reader := bufio.NewReader(io.LimitReader(stdout, int64(maxOut)))
	var result InvokeResult
	result.RequestID = reqID

	for {
		line, err := readLineBounded(ctx, reader, sdk.MaxFrameBytes)
		if err != nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_ = cmd.Wait()
			result.Diagnostics = stderr.String()
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var frame sdk.Frame
		if err := json.Unmarshal(line, &frame); err != nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_ = cmd.Wait()
			result.Diagnostics = stderr.String()
			return result, fmt.Errorf("%w: %v", ErrMalformedFrame, err)
		}
		if frame.Protocol != sdk.ProtocolVersion {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_ = cmd.Wait()
			return result, fmt.Errorf("incompatible protocol version %d", frame.Protocol)
		}
		if frame.RequestID != "" && frame.RequestID != reqID {
			continue
		}

		switch frame.Kind {
		case "progress":
			if cfg.OnProgress != nil {
				cfg.OnProgress(frame.Percent, frame.Message)
			}
		case "result":
			result.Output = frame.Output
			_ = stdin.Close()
			_ = cmd.Wait()
			result.Diagnostics = stderr.String()
			return result, nil
		case "observation":
			result.Observation = frame.Observation
			_ = stdin.Close()
			_ = cmd.Wait()
			result.Diagnostics = stderr.String()
			return result, nil
		case "error":
			_ = stdin.Close()
			_ = cmd.Wait()
			result.Diagnostics = stderr.String()
			if frame.Error != nil {
				return result, frame.Error
			}
			return result, errors.New("plugin returned error frame")
		default:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_ = cmd.Wait()
			return result, fmt.Errorf("%w: unknown frame kind %q", ErrMalformedFrame, frame.Kind)
		}
	}
}

func readFrameLine(ctx context.Context, r io.Reader, limit int) (sdk.Frame, error) {
	reader := bufio.NewReader(r)
	line, err := readLineBounded(ctx, reader, limit)
	if err != nil {
		return sdk.Frame{}, err
	}
	var frame sdk.Frame
	if err := json.Unmarshal(line, &frame); err != nil {
		return sdk.Frame{}, fmt.Errorf("%w: %v", ErrMalformedFrame, err)
	}
	return frame, nil
}

func readLineBounded(ctx context.Context, reader *bufio.Reader, limit int) ([]byte, error) {
	type readRes struct {
		line []byte
		err  error
	}
	ch := make(chan readRes, 1)
	go func() {
		var buf []byte
		for {
			frag, isPrefix, err := reader.ReadLine()
			if len(buf)+len(frag) > limit {
				ch <- readRes{err: ErrOversizedFrame}
				return
			}
			buf = append(buf, frag...)
			if !isPrefix || err != nil {
				ch <- readRes{line: buf, err: err}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}
