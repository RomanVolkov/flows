package host

import (
	"bufio"
	"bytes"
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
	"sync/atomic"
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
	id         string
	out        chan Frame
	screenOnly bool
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
		prefix := "/v1/sessions/" + h.cfg.SessionID
		preview := r.URL.Path == prefix+"/preview"
		if !preview && r.URL.Path != prefix+"/attach" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.serveAttach(w, r, preview)
	})
}

func (h *Host) serveAttach(w http.ResponseWriter, r *http.Request, preview bool) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(maxFramePayload + frameHeaderSize))
	defer conn.CloseNow()

	q := r.URL.Query()
	clientID := q.Get("client_id")
	if preview || clientID == "" {
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
	obs.screenOnly = false
	var screenReplay []byte
	if q.Get("screen") == "true" {
		if state, ok := h.cfg.State.(interface{ SnapshotFrame() ([]byte, error) }); ok {
			if frame, err := state.SnapshotFrame(); err == nil && len(frame) > 0 && json.Valid(frame) {
				screenReplay = frame
				obs.screenOnly = true
			}
		}
	}
	var replay []byte
	if !obs.screenOnly {
		replay = h.snapshotLocked()
	}
	scrolledRows := 0
	if scroller, ok := h.cfg.State.(interface{ ScrolledRows() int }); ok {
		scrolledRows = scroller.ScrolledRows()
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
	if !finished && !preview && h.ownerID == "" {
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
	if obs.screenOnly {
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
	if err := writeWSFrame(ctx, conn, controlFrame(ControlMessage{Type: "replay_end", Seq: seq, Rows: scrolledRows}, seq)); err != nil {
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
			if preview || msgType != websocket.MessageBinary {
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
			h.mu.Lock()
			if state, ok := h.cfg.State.(interface{ Scroll(int) (bool, error) }); ok {
				if moved, err := state.Scroll(ctrl.Rows); err == nil && moved {
					h.flushScreenLocked(true)
				}
			}
			h.mu.Unlock()
		case "scroll_offset":
			h.mu.Lock()
			if state, ok := h.cfg.State.(interface{ SetScrolledRows(int) error }); ok {
				if err := state.SetScrolledRows(ctrl.Rows); err == nil {
					for _, obs := range h.observers {
						if obs.screenOnly {
							h.flushScreenLocked(true)
							break
						}
					}
				}
			}
			h.mu.Unlock()
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
	Preview    bool
	ScreenOnly bool
	ClientID   string
	Epoch      uint64
	Nonce      string
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
	if opts.ScreenOnly {
		q.Set("screen", "true")
	}
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
	if opts.Preview {
		rawURL = strings.TrimSuffix(rawURL, "/attach") + "/preview"
	}
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

// NativeAttachOptions configures AttachNative for a native Ghostty surface relay.
type NativeAttachOptions struct {
	ClientID      string
	Nonce         string
	Epoch         uint64
	ControlSocket string
}

// NativeAttachOptionsFromEnv reads native attach configuration from environment variables.
func NativeAttachOptionsFromEnv() NativeAttachOptions {
	var epoch uint64
	if raw := os.Getenv("FLOWS_ATTACH_EPOCH"); raw != "" {
		epoch, _ = strconv.ParseUint(raw, 10, 64)
	}
	return NativeAttachOptions{
		ClientID:      os.Getenv("FLOWS_ATTACH_CLIENT_ID"),
		Nonce:         os.Getenv("FLOWS_ATTACH_NONCE"),
		Epoch:         epoch,
		ControlSocket: os.Getenv("FLOWS_ATTACH_CTL"),
	}
}

type nativeControlEvent struct {
	Type       string `json:"type"`
	ClientID   string `json:"client_id,omitempty"`
	OwnerID    string `json:"owner_id,omitempty"`
	Epoch      uint64 `json:"epoch,omitempty"`
	IsOwner    bool   `json:"is_owner,omitempty"`
	Scrolled   int    `json:"scrolled,omitempty"`
	Cols       uint16 `json:"cols,omitempty"`
	Rows       uint16 `json:"rows,omitempty"`
	Reason     string `json:"reason,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	ExitSignal string `json:"exit_signal,omitempty"`
}

type nativeControlCommand struct {
	Type string `json:"type"`
	Rows int    `json:"rows,omitempty"`
}

// AttachTTY attaches a local TTY (such as CLI) to the host over the shared
// WebSocket attach protocol.
func AttachTTY(ctx context.Context, socketPath, sessionID string, ttyIn, ttyOut *os.File) error {
	return attachTTYWithOptions(ctx, socketPath, sessionID, ttyIn, ttyOut, NativeAttachOptions{})
}

// AttachNative attaches a native Ghostty surface relay to the host over the
// shared WebSocket attach protocol, bridging lifecycle, input ownership, and
// scrollback offset over opts.ControlSocket when configured.
func AttachNative(ctx context.Context, socketPath, sessionID string, ttyIn, ttyOut *os.File, opts NativeAttachOptions) error {
	return attachTTYWithOptions(ctx, socketPath, sessionID, ttyIn, ttyOut, opts)
}

func attachTTYWithOptions(ctx context.Context, socketPath, sessionID string, ttyIn, ttyOut *os.File, opts NativeAttachOptions) error {
	nativeMode := opts.ControlSocket != ""
	clientID := opts.ClientID
	if nativeMode && clientID == "" {
		var err error
		clientID, err = randomHex(8)
		if err != nil {
			return err
		}
	}

	var ctlConn net.Conn
	var ctlMu sync.Mutex
	sendCtl := func(evt nativeControlEvent) {
		if ctlConn == nil {
			return
		}
		data, err := json.Marshal(evt)
		if err != nil {
			return
		}
		ctlMu.Lock()
		_, _ = ctlConn.Write(append(data, '\n'))
		ctlMu.Unlock()
	}
	if nativeMode {
		var err error
		ctlConn, err = net.DialTimeout("unix", opts.ControlSocket, 3*time.Second)
		if err != nil {
			return fmt.Errorf("dial native control socket: %w", err)
		}
		defer ctlConn.Close()
	}

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	client, err := DialAttach(relayCtx, socketPath, sessionID, AttachOptions{
		ClientID: clientID,
		Nonce:    opts.Nonce,
		Epoch:    opts.Epoch,
	})
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

	var hostSize pty.Winsize
	var scrolledRows int
	var initialOwner *InputOwnerEvent

	// Wait for replay_end before syncing with the terminal and pumping input.
	for {
		frame, err := client.ReadFrame(relayCtx)
		if err != nil {
			return err
		}
		switch frame.Kind {
		case FrameSize:
			if sz, err := decodeWinsize(frame.Payload); err == nil {
				hostSize = sz
			}
		case FrameOutput:
			if _, err := ttyOut.Write(frame.Payload); err != nil {
				return err
			}
		case FrameInputOwner:
			var evt InputOwnerEvent
			if json.Unmarshal(frame.Payload, &evt) == nil {
				initialOwner = &evt
			}
		case FrameControl:
			var ctrl ControlMessage
			if json.Unmarshal(frame.Payload, &ctrl) == nil {
				if ctrl.Type == "fenced" {
					sendCtl(nativeControlEvent{Type: "fenced", Reason: ctrl.Reason})
					return fmt.Errorf("attach fenced: %s", ctrl.Reason)
				}
				if ctrl.Type == "replay_end" {
					scrolledRows = ctrl.Rows
					goto syncTerminal
				}
			}
		case FrameExit:
			var evt ExitEvent
			if json.Unmarshal(frame.Payload, &evt) == nil {
				sendCtl(nativeControlEvent{Type: "exit", ExitCode: evt.ExitCode, ExitSignal: evt.ExitSignal})
			}
			return nil
		}
	}

syncTerminal:
	ttyReads := make(chan []byte, 32)
	ttyErr := make(chan error, 1)
	go func() {
		buf := make([]byte, maxFramePayload)
		for {
			n, err := ttyIn.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				select {
				case ttyReads <- chunk:
				case <-relayCtx.Done():
					return
				}
			}
			if err != nil {
				select {
				case ttyErr <- err:
				default:
				}
				return
			}
		}
	}()

	var pendingInput []byte
	if nativeMode && term.IsTerminal(int(ttyIn.Fd())) {
		var ok bool
		pendingInput, ok, err = syncTerminalDSR(relayCtx, ttyOut, ttyReads, ttyErr)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	var isOwner atomic.Bool
	sendSize := func() {
		if nativeMode && !isOwner.Load() {
			return
		}
		if size, err := pty.GetsizeFull(ttyIn); err == nil && size.Cols > 0 && size.Rows > 0 {
			_ = client.SendResize(relayCtx, size.Cols, size.Rows, size.X, size.Y)
		}
	}
	handleOwner := func(evt InputOwnerEvent) {
		nowOwner := !nativeMode || (evt.OwnerID == clientID && clientID != "")
		wasOwner := isOwner.Swap(nowOwner)
		sendCtl(nativeControlEvent{
			Type:     "input_owner",
			ClientID: clientID,
			OwnerID:  evt.OwnerID,
			Epoch:    evt.Epoch,
			IsOwner:  nowOwner,
		})
		if nowOwner && !wasOwner {
			sendSize()
		}
	}

	if nativeMode {
		sendCtl(nativeControlEvent{
			Type:     "ready",
			ClientID: clientID,
			Scrolled: scrolledRows,
			Cols:     hostSize.Cols,
			Rows:     hostSize.Rows,
		})
	} else {
		sendSize()
	}
	if initialOwner != nil {
		handleOwner(*initialOwner)
	}

	var detached atomic.Bool
	if ctlConn != nil {
		go runNativeControlReader(relayCtx, ctlConn, client, &detached, relayCancel)
	}

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
		if len(pendingInput) > 0 && (!nativeMode || isOwner.Load()) {
			if writeErr := client.WriteInput(relayCtx, pendingInput); writeErr != nil {
				return
			}
		}
		for {
			select {
			case <-done:
				return
			case chunk := <-ttyReads:
				if nativeMode && !isOwner.Load() {
					continue
				}
				if writeErr := client.WriteInput(relayCtx, chunk); writeErr != nil {
					return
				}
			case <-ttyErr:
				return
			}
		}
	}()

	for {
		frame, err := client.ReadFrame(relayCtx)
		if err != nil {
			if detached.Load() {
				return nil
			}
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
		case FrameInputOwner:
			var evt InputOwnerEvent
			if json.Unmarshal(frame.Payload, &evt) == nil {
				handleOwner(evt)
			}
		case FrameControl:
			var ctrl ControlMessage
			if json.Unmarshal(frame.Payload, &ctrl) == nil && ctrl.Type == "fenced" {
				sendCtl(nativeControlEvent{Type: "fenced", Reason: ctrl.Reason})
			}
		case FrameExit:
			var evt ExitEvent
			if json.Unmarshal(frame.Payload, &evt) == nil {
				sendCtl(nativeControlEvent{Type: "exit", ExitCode: evt.ExitCode, ExitSignal: evt.ExitSignal})
			}
			return nil
		}
	}
}

// syncTerminalDSR waits for the local Ghostty surface to acknowledge all replayed VT bytes via DSR.
func syncTerminalDSR(ctx context.Context, ttyOut *os.File, ttyReads <-chan []byte, ttyErr <-chan error) ([]byte, bool, error) {
	if _, err := ttyOut.Write([]byte("\x1b[5n")); err != nil {
		return nil, false, err
	}
	var dsrBuf []byte
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case chunk := <-ttyReads:
			dsrBuf = append(dsrBuf, chunk...)
			if idx := bytes.Index(dsrBuf, []byte("\x1b[0n")); idx >= 0 {
				rest := make([]byte, 0, len(dsrBuf)-4)
				rest = append(rest, dsrBuf[:idx]...)
				rest = append(rest, dsrBuf[idx+4:]...)
				return rest, true, nil
			}
		case <-timer.C:
			return dsrBuf, true, nil
		case <-ttyErr:
			return nil, false, nil
		case <-ctx.Done():
			return nil, false, nil
		}
	}
}

func runNativeControlReader(ctx context.Context, ctlConn net.Conn, client *AttachConn, detached *atomic.Bool, cancel context.CancelFunc) {
	scanner := bufio.NewScanner(ctlConn)
	for scanner.Scan() {
		var cmd nativeControlCommand
		if json.Unmarshal(scanner.Bytes(), &cmd) != nil {
			continue
		}
		switch cmd.Type {
		case "claim_input":
			_ = client.SendControl(ctx, ControlMessage{Type: "claim_input"})
		case "scroll_offset":
			_ = client.SendControl(ctx, ControlMessage{Type: "scroll_offset", Rows: cmd.Rows})
		case "detach":
			detached.Store(true)
			cancel()
			return
		}
	}
	detached.Store(true)
	cancel()
}
