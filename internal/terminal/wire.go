package terminal

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/creack/pty"
)

type messageKind uint8

const (
	output messageKind = iota + 1
	input
	windowSize
	replayEnd

	maxFrame = 32 * 1024
)

func readFrame(r io.Reader) (messageKind, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("terminal frame too large: %d", n)
	}
	payload := make([]byte, n)
	_, err := io.ReadFull(r, payload)
	return messageKind(header[0]), payload, err
}

func writeFrame(w io.Writer, kind messageKind, payload []byte) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("terminal frame too large: %d", len(payload))
	}
	var header [5]byte
	header[0] = byte(kind)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func encodeSize(size *pty.Winsize) []byte {
	var payload [8]byte
	binary.BigEndian.PutUint16(payload[:2], size.Rows)
	binary.BigEndian.PutUint16(payload[2:4], size.Cols)
	binary.BigEndian.PutUint16(payload[4:6], size.X)
	binary.BigEndian.PutUint16(payload[6:8], size.Y)
	return payload[:]
}

func decodeSize(payload []byte) (pty.Winsize, error) {
	if len(payload) != 8 {
		return pty.Winsize{}, fmt.Errorf("invalid terminal size frame")
	}
	size := pty.Winsize{
		Rows: binary.BigEndian.Uint16(payload[:2]),
		Cols: binary.BigEndian.Uint16(payload[2:4]),
		X:    binary.BigEndian.Uint16(payload[4:6]),
		Y:    binary.BigEndian.Uint16(payload[6:8]),
	}
	if size.Rows == 0 || size.Cols == 0 {
		return pty.Winsize{}, fmt.Errorf("terminal size must be positive")
	}
	return size, nil
}
