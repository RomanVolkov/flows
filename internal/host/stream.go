package host

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// FrameKind identifies binary WebSocket terminal frames.
type FrameKind uint8

const (
	FrameOutput FrameKind = iota + 1
	FrameInput
	FrameSize
	FrameControl
	FrameInputOwner
	FrameExit
	FrameScreen

	frameHeaderSize = 13
	maxFramePayload = 8 * 1024 * 1024
	maxPTYRead      = 32 * 1024
)

// Frame is a binary terminal protocol frame exchanged over /v1/sessions/{id}/attach.
type Frame struct {
	Kind    FrameKind `json:"kind"`
	Seq     uint64    `json:"seq"`
	Payload []byte    `json:"payload"`
}

// ControlMessage is the JSON payload for FrameControl.
type ControlMessage struct {
	Type   string `json:"type"`
	Seq    uint64 `json:"seq,omitempty"`
	Reason string `json:"reason,omitempty"`
	Rows   int    `json:"rows,omitempty"`
}

// InputOwnerEvent is the JSON payload for FrameInputOwner.
type InputOwnerEvent struct {
	OwnerID string `json:"owner_id"`
	Epoch   uint64 `json:"epoch"`
}

// ExitEvent is the JSON payload for FrameExit.
type ExitEvent struct {
	ExitCode   int    `json:"exit_code"`
	ExitSignal string `json:"exit_signal,omitempty"`
	Seq        uint64 `json:"seq"`
}

type attachObserver struct {
	id  string
	out chan Frame
}

func encodeFrame(f Frame) ([]byte, error) {
	if len(f.Payload) > maxFramePayload {
		return nil, fmt.Errorf("frame payload too large: %d", len(f.Payload))
	}
	buf := make([]byte, frameHeaderSize+len(f.Payload))
	buf[0] = byte(f.Kind)
	binary.BigEndian.PutUint64(buf[1:9], f.Seq)
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(f.Payload)))
	copy(buf[frameHeaderSize:], f.Payload)
	return buf, nil
}

func decodeFrame(data []byte) (Frame, error) {
	if len(data) < frameHeaderSize {
		return Frame{}, fmt.Errorf("short terminal frame: %d bytes", len(data))
	}
	length := binary.BigEndian.Uint32(data[9:13])
	if int(length) != len(data)-frameHeaderSize {
		return Frame{}, fmt.Errorf("terminal frame length mismatch: header=%d body=%d", length, len(data)-frameHeaderSize)
	}
	if length > maxFramePayload {
		return Frame{}, fmt.Errorf("terminal frame exceeds max payload: %d", length)
	}
	return Frame{
		Kind:    FrameKind(data[0]),
		Seq:     binary.BigEndian.Uint64(data[1:9]),
		Payload: append([]byte(nil), data[frameHeaderSize:]...),
	}, nil
}

func encodeWinsize(size pty.Winsize) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint16(buf[0:2], size.Rows)
	binary.BigEndian.PutUint16(buf[2:4], size.Cols)
	binary.BigEndian.PutUint16(buf[4:6], size.X)
	binary.BigEndian.PutUint16(buf[6:8], size.Y)
	return buf[:]
}

func decodeWinsize(payload []byte) (pty.Winsize, error) {
	if len(payload) != 8 {
		return pty.Winsize{}, fmt.Errorf("invalid terminal size payload")
	}
	size := pty.Winsize{
		Rows: binary.BigEndian.Uint16(payload[0:2]),
		Cols: binary.BigEndian.Uint16(payload[2:4]),
		X:    binary.BigEndian.Uint16(payload[4:6]),
		Y:    binary.BigEndian.Uint16(payload[6:8]),
	}
	if size.Rows == 0 || size.Cols == 0 {
		return pty.Winsize{}, fmt.Errorf("terminal dimensions must be positive")
	}
	return size, nil
}

func (h *Host) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, ok := parseAttachPath(r.URL.Path)
		if !ok || sessionID != h.cfg.SessionID {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.serveAttach(w, r)
	})
}

func parseAttachPath(path string) (string, bool) {
	trimmed := strings.TrimPrefix(path, "/v1/sessions/")
	if trimmed == path {
		return "", false
	}
	sessionID, ok := strings.CutSuffix(trimmed, "/attach")
	if !ok || sessionID == "" || strings.Contains(sessionID, "/") {
		return "", false
	}
	return sessionID, true
}

func (h *Host) serveAttach(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(maxFramePayload + frameHeaderSize))
	defer conn.CloseNow()

	q := r.URL.Query()
	clientID := q.Get("client_id")
	if clientID == "" {
		var err error
		clientID, err = randomHex(8)
		if err != nil {
			return
		}
	}
	if nonce := q.Get("nonce"); nonce != "" && nonce != h.record.StartNonce {
		_ = writeWSFrame(r.Context(), conn, controlFrame(ControlMessage{Type: "fenced", Reason: "stale_nonce"}, 0))
		_ = conn.Close(websocket.StatusPolicyViolation, "stale host nonce")
		return
	}
	var requestedEpoch uint64
	if raw := q.Get("epoch"); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			requestedEpoch = parsed
		}
	}

	obs := &attachObserver{
		id:  clientID,
		out: make(chan Frame, 128),
	}

	h.mu.Lock()
	if requestedEpoch > 0 && requestedEpoch < h.ownerEpoch {
		h.mu.Unlock()
		_ = writeWSFrame(r.Context(), conn, controlFrame(ControlMessage{Type: "fenced", Reason: "stale_epoch"}, 0))
		_ = conn.Close(websocket.StatusPolicyViolation, "stale attach epoch")
		return
	}
	replay := h.snapshotLocked()
	var screenReplay []byte
	if state, ok := h.cfg.State.(interface{ SnapshotFrame() ([]byte, error) }); ok {
		screenReplay, _ = state.SnapshotFrame()
	}
	seq := h.record.StreamSeq
	cols, rows := h.record.Cols, h.record.Rows
	finished := h.finished
	var exitEvt *ExitEvent
	if finished && h.record.ExitCode != nil {
		exitEvt = &ExitEvent{
			ExitCode:   *h.record.ExitCode,
			ExitSignal: h.record.ExitSignal,
			Seq:        seq,
		}
	}
	ownerChanged := false
	if !finished && h.ownerID == "" {
		h.ownerEpoch++
		h.ownerID = clientID
		ownerChanged = true
	}
	ownerID, ownerEpoch := h.ownerID, h.ownerEpoch
	if !finished {
		h.observers[clientID] = obs
		if ownerChanged {
			payload, _ := json.Marshal(InputOwnerEvent{OwnerID: ownerID, Epoch: ownerEpoch})
			h.broadcastLocked(Frame{Kind: FrameInputOwner, Seq: seq, Payload: payload})
		}
	}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.observers, clientID)
		if h.ownerID == clientID {
			h.ownerID = ""
			h.ownerEpoch++
			payload, _ := json.Marshal(InputOwnerEvent{OwnerID: "", Epoch: h.ownerEpoch})
			h.broadcastLocked(Frame{Kind: FrameInputOwner, Seq: h.record.StreamSeq, Payload: payload})
		}
		h.mu.Unlock()
	}()

	ctx := r.Context()
	if err := writeWSFrame(ctx, conn, Frame{
		Kind:    FrameSize,
		Seq:     seq,
		Payload: encodeWinsize(pty.Winsize{Rows: rows, Cols: cols}),
	}); err != nil {
		return
	}
	if len(screenReplay) > 0 {
		if err := writeWSFrame(ctx, conn, Frame{Kind: FrameScreen, Seq: seq, Payload: screenReplay}); err != nil {
			return
		}
	} else {
		for off := 0; off < len(replay); off += maxFramePayload {
			end := min(off+maxFramePayload, len(replay))
			if err := writeWSFrame(ctx, conn, Frame{
				Kind:    FrameOutput,
				Seq:     seq,
				Payload: replay[off:end],
			}); err != nil {
				return
			}
		}
	}
	if err := writeWSFrame(ctx, conn, controlFrame(ControlMessage{Type: "replay_end", Seq: seq}, seq)); err != nil {
		return
	}
	ownerPayload, _ := json.Marshal(InputOwnerEvent{OwnerID: ownerID, Epoch: ownerEpoch})
	if err := writeWSFrame(ctx, conn, Frame{Kind: FrameInputOwner, Seq: seq, Payload: ownerPayload}); err != nil {
		return
	}

	if finished {
		if exitEvt != nil {
			payload, _ := json.Marshal(exitEvt)
			_ = writeWSFrame(ctx, conn, Frame{Kind: FrameExit, Seq: seq, Payload: payload})
		}
		_ = conn.Close(websocket.StatusNormalClosure, "session exited")
		return
	}

	var writeMu sync.Mutex
	sendFrame := func(f Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeWSFrame(ctx, conn, f)
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			msgType, data, err := conn.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			if msgType != websocket.MessageBinary {
				continue
			}
			frame, err := decodeFrame(data)
			if err != nil {
				readErr <- err
				return
			}
			if err := h.handleClientFrame(clientID, frame, sendFrame); err != nil {
				readErr <- err
				return
			}
		}
	}()

	for {
		select {
		case frame, ok := <-obs.out:
			if !ok {
				_ = conn.Close(websocket.StatusNormalClosure, "host closed")
				return
			}
			if err := sendFrame(frame); err != nil {
				return
			}
			if frame.Kind == FrameExit {
				_ = conn.Close(websocket.StatusNormalClosure, "session exited")
				return
			}
		case <-readErr:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (h *Host) handleClientFrame(clientID string, frame Frame, reply func(Frame) error) error {
	switch frame.Kind {
	case FrameInput:
		h.mu.Lock()
		isOwner := h.ownerID == clientID
		seq := h.record.StreamSeq
		h.mu.Unlock()
		if !isOwner {
			return reply(controlFrame(ControlMessage{Type: "fenced", Reason: "not_input_owner"}, seq))
		}
		_, err := h.pty.Write(frame.Payload)
		return err
	case FrameSize:
		h.mu.Lock()
		isOwner := h.ownerID == clientID
		seq := h.record.StreamSeq
		h.mu.Unlock()
		if !isOwner {
			return reply(controlFrame(ControlMessage{Type: "fenced", Reason: "not_input_owner"}, seq))
		}
		size, err := decodeWinsize(frame.Payload)
		if err != nil {
			return err
		}
		return h.Resize(size.Cols, size.Rows, size.X, size.Y)
	case FrameControl:
		var ctrl ControlMessage
		if err := json.Unmarshal(frame.Payload, &ctrl); err != nil {
			return nil
		}
		switch ctrl.Type {
		case "claim_input":
			h.mu.Lock()
			if !h.finished {
				h.ownerEpoch++
				h.ownerID = clientID
				payload, _ := json.Marshal(InputOwnerEvent{OwnerID: h.ownerID, Epoch: h.ownerEpoch})
				h.broadcastLocked(Frame{Kind: FrameInputOwner, Seq: h.record.StreamSeq, Payload: payload})
			}
			h.mu.Unlock()
		case "interrupt":
			h.mu.Lock()
			isOwner := h.ownerID == clientID
			h.mu.Unlock()
			if isOwner {
				_ = h.Interrupt()
			}
		case "stop":
			h.mu.Lock()
			isOwner := h.ownerID == clientID
			h.mu.Unlock()
			if isOwner {
				go func() { _ = h.RequestStop(2 * time.Second) }()
			}
		case "scroll":
			if state, ok := h.cfg.State.(interface{ Scroll(int) (bool, error) }); ok {
				if _, err := state.Scroll(ctrl.Rows); err == nil {
					if render, ok := h.cfg.State.(interface{ RenderFrame(bool) ([]byte, error) }); ok {
						if payload, err := render.RenderFrame(true); err == nil {
							h.mu.Lock()
							h.broadcastLocked(Frame{Kind: FrameScreen, Seq: h.record.StreamSeq, Payload: payload})
							h.mu.Unlock()
						}
					}
				}
			}
		}
	}
	return nil
}

func controlFrame(msg ControlMessage, seq uint64) Frame {
	payload, _ := json.Marshal(msg)
	return Frame{Kind: FrameControl, Seq: seq, Payload: payload}
}

func writeWSFrame(ctx context.Context, conn *websocket.Conn, f Frame) error {
	raw, err := encodeFrame(f)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageBinary, raw)
}

// AttachOptions configures a WebSocket attach client.
type AttachOptions struct {
	ClientID string
	Epoch    uint64
	Nonce    string
}

// AttachConn is a client WebSocket connection to /v1/sessions/{id}/attach.
type AttachConn struct {
	conn *websocket.Conn
}

// DialAttach opens an HTTP/1.1 GET /v1/sessions/{id}/attach WebSocket over socketPath.
func DialAttach(ctx context.Context, socketPath, sessionID string, opts AttachOptions) (*AttachConn, error) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	q := url.Values{}
	if opts.ClientID != "" {
		q.Set("client_id", opts.ClientID)
	}
	if opts.Epoch > 0 {
		q.Set("epoch", strconv.FormatUint(opts.Epoch, 10))
	}
	if opts.Nonce != "" {
		q.Set("nonce", opts.Nonce)
	}
	rawURL := "ws://flows.local/v1/sessions/" + url.PathEscape(sessionID) + "/attach"
	if encoded := q.Encode(); encoded != "" {
		rawURL += "?" + encoded
	}
	conn, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(int64(maxFramePayload + frameHeaderSize))
	return &AttachConn{conn: conn}, nil
}

// ReadFrame reads the next binary protocol frame from the host.
func (c *AttachConn) ReadFrame(ctx context.Context) (Frame, error) {
	for {
		msgType, data, err := c.conn.Read(ctx)
		if err != nil {
			return Frame{}, err
		}
		if msgType != websocket.MessageBinary {
			continue
		}
		return decodeFrame(data)
	}
}

// WriteInput sends terminal input bytes to the host.
func (c *AttachConn) WriteInput(ctx context.Context, data []byte) error {
	for off := 0; off < len(data); off += maxFramePayload {
		end := min(off+maxFramePayload, len(data))
		if err := writeWSFrame(ctx, c.conn, Frame{Kind: FrameInput, Payload: data[off:end]}); err != nil {
			return err
		}
	}
	return nil
}

// SendResize sends updated terminal dimensions to the host.
func (c *AttachConn) SendResize(ctx context.Context, cols, rows, x, y uint16) error {
	return writeWSFrame(ctx, c.conn, Frame{
		Kind:    FrameSize,
		Payload: encodeWinsize(pty.Winsize{Rows: rows, Cols: cols, X: x, Y: y}),
	})
}

// SendControl sends a JSON control frame to the host.
func (c *AttachConn) SendControl(ctx context.Context, msg ControlMessage) error {
	return writeWSFrame(ctx, c.conn, controlFrame(msg, 0))
}

// Close closes the WebSocket connection cleanly.
func (c *AttachConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "client detached")
}

// AttachTTY attaches a local TTY (such as CLI or native Ghostty relay) to the host
// over the shared WebSocket attach protocol.
func AttachTTY(ctx context.Context, socketPath, sessionID string, ttyIn, ttyOut *os.File) error {
	client, err := DialAttach(ctx, socketPath, sessionID, AttachOptions{})
	if err != nil {
		return err
	}
	defer client.Close()

	if term.IsTerminal(int(ttyIn.Fd())) {
		oldState, err := term.MakeRaw(int(ttyIn.Fd()))
		if err != nil {
			return fmt.Errorf("put terminal in raw mode: %w", err)
		}
		defer term.Restore(int(ttyIn.Fd()), oldState)
	}

	resizes := make(chan os.Signal, 1)
	signal.Notify(resizes, syscall.SIGWINCH)
	defer signal.Stop(resizes)

	// Wait for replay_end before sending initial size and pumping input.
	for {
		frame, err := client.ReadFrame(ctx)
		if err != nil {
			return err
		}
		switch frame.Kind {
		case FrameOutput:
			if _, err := ttyOut.Write(frame.Payload); err != nil {
				return err
			}
		case FrameControl:
			var ctrl ControlMessage
			if json.Unmarshal(frame.Payload, &ctrl) == nil && ctrl.Type == "replay_end" {
				goto live
			}
		case FrameExit:
			return nil
		}
	}

live:
	sendSize := func() {
		if size, err := pty.GetsizeFull(ttyIn); err == nil && size.Cols > 0 && size.Rows > 0 {
			_ = client.SendResize(ctx, size.Cols, size.Rows, size.X, size.Y)
		}
	}
	sendSize()

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-resizes:
				sendSize()
			}
		}
	}()

	go func() {
		buf := make([]byte, maxFramePayload)
		for {
			n, err := ttyIn.Read(buf)
			if n > 0 {
				if writeErr := client.WriteInput(ctx, buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		frame, err := client.ReadFrame(ctx)
		if err != nil {
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) && closeErr.Code == websocket.StatusNormalClosure {
				return nil
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch frame.Kind {
		case FrameOutput:
			if _, err := ttyOut.Write(frame.Payload); err != nil {
				return err
			}
		case FrameExit:
			return nil
		}
	}
}
