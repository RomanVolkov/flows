package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// TerminalState tracks the canonical terminal even when no renderer is
// connected. Replay must return display sequences without terminal queries.
type TerminalState interface {
	Feed(data []byte, replyTo *os.File)
	Resize(size pty.Winsize) error
	Replay() ([]byte, error)
}

type observer struct {
	net.Conn
	outputs chan []byte
	done    chan struct{}
}

type host struct {
	mu       sync.Mutex
	pty      *os.File
	state    TerminalState
	active   *observer
	finished bool
}

// Serve starts the child in a dedicated PTY and accepts a renderer over a
// private local socket until the child exits or the context is cancelled.
func Serve(ctx context.Context, socket string, cmd *exec.Cmd, state TerminalState) error {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	winsize := pty.Winsize{Rows: 24, Cols: 80}
	if err := state.Resize(winsize); err != nil {
		return err
	}
	master, err := pty.StartWithSize(cmd, &winsize)
	if err != nil {
		return err
	}
	defer master.Close()
	unblock := context.AfterFunc(ctx, func() {
		listener.Close()
		master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	defer unblock()

	h := &host{pty: master, state: state}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		h.readPTY()
		listener.Close()
	}()

	var peers sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		peers.Go(func() {
			defer conn.Close()
			h.attach(conn)
		})
	}

	<-drained
	peers.Wait()
	if err := cmd.Wait(); ctx.Err() != nil {
		return ctx.Err()
	} else {
		return err
	}
}

func (h *host) readPTY() {
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.finished = true
		if h.active != nil {
			close(h.active.outputs)
		}
	}()
	buf := make([]byte, maxFrame)
	for {
		n, err := h.pty.Read(buf)
		if n > 0 {
			h.publish(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (h *host) publish(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active != nil {
		select {
		case h.active.outputs <- append([]byte(nil), data...):
			h.state.Feed(data, nil)
			return
		default:
			h.active.Close()
			h.active = nil
		}
	}
	h.state.Feed(data, h.pty)
}

func (h *host) attach(conn net.Conn) {
	h.mu.Lock()
	if h.active != nil || h.finished {
		h.mu.Unlock()
		return
	}
	replayBytes, err := h.state.Replay()
	if err != nil {
		h.mu.Unlock()
		return
	}
	client := &observer{Conn: conn, outputs: make(chan []byte, 64), done: make(chan struct{})}
	h.active = client
	h.mu.Unlock()
	written := make(chan struct{})
	go func() {
		defer close(written)
		defer conn.Close()
		if err := streamOutput(client, replayBytes); err != nil {
			return
		}
	}()
	for {
		kind, data, err := readFrame(conn)
		if err != nil || h.handleInput(client, kind, data) != nil {
			break
		}
	}
	h.mu.Lock()
	if h.active == client {
		h.active = nil
	}
	h.mu.Unlock()
	close(client.done)
	conn.Close()
	<-written
}

func streamOutput(client *observer, replayBytes []byte) error {
	send := func(kind messageKind, payload []byte) error {
		if err := client.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return writeFrame(client, kind, payload)
	}
	for len(replayBytes) > 0 {
		n := min(len(replayBytes), maxFrame)
		if err := send(output, replayBytes[:n]); err != nil {
			return err
		}
		replayBytes = replayBytes[n:]
	}
	if err := send(replayEnd, nil); err != nil {
		return err
	}
	for {
		select {
		case <-client.done:
			return nil
		case data, open := <-client.outputs:
			if !open {
				return nil
			}
			if err := send(output, data); err != nil {
				return err
			}
		}
	}
}

func (h *host) handleInput(client *observer, kind messageKind, data []byte) error {
	switch kind {
	case input:
		h.mu.Lock()
		valid := h.active == client && !h.finished
		h.mu.Unlock()
		if !valid {
			return io.ErrClosedPipe
		}
		_, err := h.pty.Write(data)
		return err
	case windowSize:
		size, err := decodeSize(data)
		if err != nil {
			return err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.active != client || h.finished {
			return io.ErrClosedPipe
		}
		return errors.Join(h.state.Resize(size), pty.Setsize(h.pty, &size))
	default:
		return fmt.Errorf("unexpected relay frame %d", kind)
	}
}
