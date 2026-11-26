//go:build ghostty

package terminal

import (
	"bytes"
	"testing"

	"github.com/creack/pty"
)

func TestGhosttyReplayRestoresModesAndText(t *testing.T) {
	source, err := NewGhosttyState()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.Feed([]byte("\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[1;1Hchild=123 雪\x1b[2;3H"), nil)
	size := pty.Winsize{Cols: 110, Rows: 63}
	if err := source.Resize(size); err != nil {
		t.Fatal(err)
	}
	source.Feed([]byte("\x1b[6;1Hresize-ok"), nil)
	replay, err := source.Replay()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"child=123 雪", "resize-ok", "\x1b[?1049h", "\x1b[?2004h", "\x1b[?1000h"} {
		if !bytes.Contains(replay, []byte(token)) {
			t.Errorf("formatted replay lacks %q", token)
		}
	}

	target, err := NewGhosttyState()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Resize(size); err != nil {
		t.Fatal(err)
	}
	target.Feed(replay, nil)
	restored, err := target.Replay()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"child=123 雪", "resize-ok", "\x1b[?1049h", "\x1b[?2004h", "\x1b[?1000h"} {
		if !bytes.Contains(restored, []byte(token)) {
			t.Errorf("restored terminal lacks %q", token)
		}
	}
}

func TestGhosttyPrimaryReplayIncludesScrollback(t *testing.T) {
	state, err := NewGhosttyState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	state.Feed([]byte("first scrollback line\r\n"), nil)
	state.Feed(bytes.Repeat([]byte("subsequent line\r\n"), 64), nil)
	replay, err := state.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(replay, []byte("first scrollback line")) {
		t.Fatal("formatted replay lost primary scrollback")
	}
}
