//go:build ghostty

package terminal

/*
#include <ghostty/vt.h>
#include <errno.h>
#include <stdlib.h>
#include <unistd.h>

typedef struct {
	GhosttyTerminal terminal;
	int reply_fd;
} flows_vt;

static void write_reply(GhosttyTerminal terminal, void *context, const uint8_t *data, size_t len) {
	flows_vt *vt = context;
	if (vt->reply_fd < 0) return;
	while (len > 0) {
		ssize_t written = write(vt->reply_fd, data, len);
		if (written < 0 && errno == EINTR) continue;
		if (written <= 0) return;
		data += written;
		len -= written;
	}
}

static flows_vt *open_vt(void) {
	flows_vt *vt = calloc(1, sizeof(flows_vt));
	if (!vt) return NULL;
	vt->reply_fd = -1;
	if (ghostty_terminal_new(NULL, &vt->terminal, 80, 24) != GHOSTTY_SUCCESS) {
		free(vt);
		return NULL;
	}
	ghostty_terminal_set(vt->terminal, GHOSTTY_TERMINAL_OPT_USERDATA, vt);
	ghostty_terminal_set(vt->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY, write_reply);
	return vt;
}

static void feed_vt(flows_vt *vt, const uint8_t *data, size_t len, int reply_fd) {
	vt->reply_fd = reply_fd;
	ghostty_terminal_vt_write(vt->terminal, data, len);
	vt->reply_fd = -1;
}

static void close_vt(flows_vt *vt) {
	ghostty_terminal_free(vt->terminal);
	free(vt);
}

static GhosttyResult replay_vt(flows_vt *vt, uint8_t **data, size_t *len) {
	GhosttyFormatterTerminalOptions options = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
	options.emit = GHOSTTY_FORMATTER_FORMAT_VT;
	options.extra = (GhosttyFormatterTerminalExtra){
		.size = sizeof(GhosttyFormatterTerminalExtra),
		.palette = true,
		.modes = true,
		.scrolling_region = true,
		.tabstops = true,
		.keyboard = true,
		.screen = {
			.size = sizeof(GhosttyFormatterScreenExtra),
			.cursor = true,
			.style = true,
			.hyperlink = true,
			.protection = true,
			.kitty_keyboard = true,
			.charsets = true,
		},
	};
	GhosttyFormatter formatter;
	GhosttyResult result = ghostty_formatter_terminal_new(NULL, &formatter, vt->terminal, options);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_formatter_format_alloc(formatter, NULL, data, len);
	ghostty_formatter_free(formatter);
	return result;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/creack/pty"
)

// GhosttyState owns the canonical VT state of the host.
type GhosttyState struct {
	vt *C.flows_vt
}

func NewGhosttyState() (*GhosttyState, error) {
	vt := C.open_vt()
	if vt == nil {
		return nil, fmt.Errorf("initialize libghostty-vt terminal")
	}
	return &GhosttyState{vt: vt}, nil
}

func (s *GhosttyState) Close() { C.close_vt(s.vt) }

func (s *GhosttyState) Feed(data []byte, replyTo *os.File) {
	fd := C.int(-1)
	if replyTo != nil {
		fd = C.int(replyTo.Fd())
	}
	C.feed_vt(s.vt, (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data))), C.size_t(len(data)), fd)
	runtime.KeepAlive(replyTo)
}

func (s *GhosttyState) Resize(size pty.Winsize) error {
	width, height := size.X/size.Cols, size.Y/size.Rows
	result := C.ghostty_terminal_resize(s.vt.terminal, C.uint16_t(size.Cols), C.uint16_t(size.Rows), C.uint32_t(width), C.uint32_t(height))
	if result != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("resize Ghostty VT: status %d", result)
	}
	return nil
}

func (s *GhosttyState) Replay() ([]byte, error) {
	var data *C.uint8_t
	var size C.size_t
	result := C.replay_vt(s.vt, &data, &size)
	if result != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("format Ghostty VT: status %d", result)
	}
	defer C.ghostty_free(nil, data, size)
	if size > 16*1024*1024 {
		return nil, fmt.Errorf("Ghostty replay exceeds 16 MiB")
	}
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), nil
}
