// Package terminal connects a Ghostty surface PTY to a separately hosted PTY.
package terminal

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func Attach(socket string, ttyIn, ttyOut *os.File) error {
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	state, err := term.MakeRaw(int(ttyIn.Fd()))
	if err != nil {
		return fmt.Errorf("put relay terminal in raw mode: %w", err)
	}
	defer term.Restore(int(ttyIn.Fd()), state)

	// Install the signal handler before taking the first size, so that a
	// resize racing the initial handshake is picked up after the barrier.
	resizes := make(chan os.Signal, 1)
	signal.Notify(resizes, syscall.SIGWINCH)
	defer signal.Stop(resizes)

	if err := replay(conn, ttyOut); err != nil {
		return err
	}
	var writes sync.Mutex
	send := func(kind messageKind, data []byte) error {
		writes.Lock()
		defer writes.Unlock()
		return writeFrame(conn, kind, data)
	}
	sendSize := func() error {
		size, err := pty.GetsizeFull(ttyIn)
		if err != nil {
			return err
		}
		return send(windowSize, encodeSize(size))
	}
	if err := sendSize(); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-resizes:
				if sendSize() != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	go func() {
		data := make([]byte, maxFrame)
		for {
			n, err := ttyIn.Read(data)
			if n > 0 && send(input, data[:n]) != nil {
				conn.Close()
				return
			}
			if err != nil {
				conn.Close()
				return
			}
		}
	}()
	for {
		kind, data, err := readFrame(conn)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if kind != output {
			return fmt.Errorf("unexpected host frame %d", kind)
		}
		if _, err := ttyOut.Write(data); err != nil {
			return err
		}
	}
}

func replay(conn net.Conn, out io.Writer) error {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	for {
		kind, data, err := readFrame(conn)
		if err != nil {
			return err
		}
		switch kind {
		case output:
			if _, err := out.Write(data); err != nil {
				return err
			}
		case replayEnd:
			return conn.SetDeadline(time.Time{})
		default:
			return fmt.Errorf("unexpected replay frame %d", kind)
		}
	}
}
