// Package builtin implements built-in command execution and user notification/question handling.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// CommandSpec configures a built-in deterministic command execution.
type CommandSpec struct {
	Executable string
	Args       []string
	WorkDir    string
	Env        map[string]string
	Timeout    time.Duration
	OnOutput   func(stream, line string)
}

// CommandResult captures the exit code, output streams, and timeout status.
type CommandResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// RunCommand executes spec with explicit executable, args, cwd, env, and timeout,
// streaming output lines to spec.OnOutput.
func RunCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if spec.Executable == "" {
		return CommandResult{}, fmt.Errorf("executable is required")
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	if len(spec.Env) > 0 {
		env := os.Environ()
		for k, v := range spec.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return CommandResult{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return CommandResult{}, err
	}

	if err := cmd.Start(); err != nil {
		return CommandResult{}, err
	}
	pgid := cmd.Process.Pid
	unblock := context.AfterFunc(ctx, func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
	defer unblock()

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	readStream := func(name string, r io.Reader, dst *bytes.Buffer) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 16*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			dst.WriteString(line)
			dst.WriteByte('\n')
			if spec.OnOutput != nil {
				spec.OnOutput(name, line)
			}
		}
	}

	wg.Add(2)
	go readStream("stdout", stdoutPipe, &stdoutBuf)
	go readStream("stderr", stderrPipe, &stderrBuf)
	wg.Wait()

	waitErr := cmd.Wait()
	res := CommandResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = 124
		return res, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, waitErr
		}
		res.ExitCode = 1
		return res, waitErr
	}
	return res, nil
}
