//go:build ghostty

package terminal

/*
#include <ghostty/vt.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>

typedef struct {
	GhosttyTerminal terminal;
	GhosttyRenderState render;
	GhosttyRenderStateRowIterator rows;
	GhosttyRenderStateRowCells cells;
	int reply_fd;
} flows_vt;

static uint32_t rgb(GhosttyColorRgb c) {
	return ((uint32_t)c.r << 16) | ((uint32_t)c.g << 8) | (uint32_t)c.b;
}

static bool mode_enabled(GhosttyTerminal terminal, GhosttyMode mode) {
	GhosttyTerminalModeConfig config = {.mode = mode, .value = false};
	return ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_MODE, &config) == GHOSTTY_SUCCESS && config.value;
}

static void json_string(FILE *f, const uint8_t *p, size_t n) {
	fputc('"', f);
	for (size_t i = 0; i < n; i++) {
		uint8_t c = p[i];
		switch (c) {
		case '"': fputs("\\\"", f); break;
		case '\\': fputs("\\\\", f); break;
		case '\b': fputs("\\b", f); break;
		case '\f': fputs("\\f", f); break;
		case '\n': fputs("\\n", f); break;
		case '\r': fputs("\\r", f); break;
		case '\t': fputs("\\t", f); break;
	default:
			if (c < 0x20) fprintf(f, "\\u%04x", c);
			else fputc(c, f);
		}
	}
	fputc('"', f);
}

static char *frame_json(flows_vt *vt, bool force_full, bool clear, size_t *length, bool *no_change) {
	if (ghostty_render_state_update(vt->render, vt->terminal) != GHOSTTY_SUCCESS) return NULL;
	uint16_t cols = 0, rows = 0;
	GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
	GhosttyRenderStateColors colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
	GhosttyRenderStateCursor cursor = GHOSTTY_INIT_SIZED(GhosttyRenderStateCursor);
	if (ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_COLS, &cols) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_ROWS, &rows) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_COLORS, &colors) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cursor) != GHOSTTY_SUCCESS) return NULL;
	bool full = force_full || dirty == GHOSTTY_RENDER_STATE_DIRTY_FULL;
	if (!full && dirty == GHOSTTY_RENDER_STATE_DIRTY_FALSE) { *no_change = true; return NULL; }
	if (ghostty_render_state_get(vt->render, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &vt->rows) != GHOSTTY_SUCCESS) return NULL;
	GhosttyTerminalScreen active_screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	ghostty_terminal_get(vt->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &active_screen);
	bool paste = mode_enabled(vt->terminal, GHOSTTY_MODE_BRACKETED_PASTE);
	bool appkey = mode_enabled(vt->terminal, GHOSTTY_MODE_DECCKM);
	bool mouse = mode_enabled(vt->terminal, GHOSTTY_MODE_X10_MOUSE) || mode_enabled(vt->terminal, GHOSTTY_MODE_NORMAL_MOUSE) ||
		mode_enabled(vt->terminal, GHOSTTY_MODE_BUTTON_MOUSE) || mode_enabled(vt->terminal, GHOSTTY_MODE_ANY_MOUSE);
	bool mousesgr = mode_enabled(vt->terminal, GHOSTTY_MODE_SGR_MOUSE);
	GhosttyTerminalScrollbar scrollbar = {0};
	ghostty_terminal_get(vt->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &scrollbar);
	uint64_t scrolled = scrollbar.total > scrollbar.offset + scrollbar.len ? scrollbar.total - scrollbar.offset - scrollbar.len : 0;
	char *data = NULL;
	FILE *f = open_memstream(&data, length);
	if (!f) return NULL;
	fprintf(f, "{\"full\":%s,\"cols\":%u,\"rows\":%u,\"fg\":%u,\"bg\":%u,\"alt\":%s,\"paste\":%s,\"appkey\":%s,\"mouse\":%s,\"mousesgr\":%s,\"scrolled\":%llu,\"cursor\":{\"x\":%u,\"y\":%u,\"visible\":%s,\"style\":\"%s\"},\"lines\":[",
		full ? "true" : "false", cols, rows, rgb(colors.foreground), rgb(colors.background),
		active_screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE ? "true" : "false", paste ? "true" : "false", appkey ? "true" : "false",
		mouse ? "true" : "false", mousesgr ? "true" : "false",
		(unsigned long long)scrolled,
		cursor.viewport_x, cursor.viewport_y,
		cursor.visible && cursor.viewport_has_value ? "true" : "false",
		cursor.visual_style == GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR ? "bar" :
		cursor.visual_style == GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE ? "underline" :
		cursor.visual_style == GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK_HOLLOW ? "hollow" : "block");
	int first_row = 1;
	uint16_t y = 0;
	while (full ? ghostty_render_state_row_iterator_next(vt->rows) : ghostty_render_state_row_iterator_next_dirty(vt->rows, &y)) {
		if (full) y = (uint16_t)(first_row ? 0 : y + 1);
		if (y >= rows) break;
		if (ghostty_render_state_row_get(vt->rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &vt->cells) != GHOSTTY_SUCCESS) { fclose(f); free(data); return NULL; }
		if (!first_row) fputc(',', f);
		first_row = 0;
		fprintf(f, "{\"y\":%u,\"cells\":[", y);
		int first_cell = 1;
		while (ghostty_render_state_row_cells_next(vt->cells)) {
			GhosttyCell raw;
			GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
			GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
			uint8_t text[512];
			GhosttyBuffer buf = {.ptr = text, .cap = sizeof(text), .len = 0};
			ghostty_render_state_row_cells_get(vt->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw);
			ghostty_render_state_row_cells_get(vt->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
			ghostty_render_state_row_cells_get(vt->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &buf);
			ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide);
			if (!first_cell) fputc(',', f);
			first_cell = 0;
			fputc('{', f);
			int field = 0;
			if (buf.len <= sizeof(text) && buf.len > 0) { fputs("\"t\":", f); json_string(f, text, buf.len); field = 1; }
			GhosttyColorRgb fg, bg;
			if (ghostty_render_state_row_cells_get(vt->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fg) == GHOSTTY_SUCCESS) {
				if (field++) fputc(',', f);
				fprintf(f, "\"f\":%u", rgb(fg) | (1u << 24));
			}
			if (ghostty_render_state_row_cells_get(vt->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg) == GHOSTTY_SUCCESS) {
				if (field++) fputc(',', f);
				fprintf(f, "\"b\":%u", rgb(bg) | (1u << 24));
			}
			uint8_t attr = (style.bold ? 1 : 0) | (style.faint ? 2 : 0) | (style.italic ? 4 : 0) |
				(style.underline != GHOSTTY_SGR_UNDERLINE_NONE ? 8 : 0) | (style.strikethrough ? 16 : 0) | (style.inverse ? 32 : 0);
			if (attr) { if (field++) fputc(',', f); fprintf(f, "\"a\":%u", attr); }
			uint8_t w = wide == GHOSTTY_CELL_WIDE_WIDE ? 1 :
				(wide == GHOSTTY_CELL_WIDE_SPACER_HEAD || wide == GHOSTTY_CELL_WIDE_SPACER_TAIL ? 2 : 0);
			if (field) fputc(',', f);
			fprintf(f, "\"w\":%u}", w);
		}
		fputs("]}", f);
	}
	fputs("]}", f);
	if (fclose(f) != 0) { free(data); return NULL; }
	if (clear && ghostty_render_state_clean(vt->render) != GHOSTTY_SUCCESS) { free(data); return NULL; }
	return data;
}

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
	if (ghostty_render_state_new(NULL, &vt->render) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_row_iterator_new(NULL, &vt->rows) != GHOSTTY_SUCCESS ||
	    ghostty_render_state_row_cells_new(NULL, &vt->cells) != GHOSTTY_SUCCESS) {
		ghostty_terminal_free(vt->terminal);
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

static void scroll_vt(flows_vt *vt, int32_t delta) {
	GhosttyTerminalScrollViewport behavior = {
		.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA,
		.value.delta = delta,
	};
	ghostty_terminal_scroll_viewport(vt->terminal, behavior);
}

static void close_vt(flows_vt *vt) {
	ghostty_render_state_row_cells_free(vt->cells);
	ghostty_render_state_row_iterator_free(vt->rows);
	ghostty_render_state_free(vt->render);
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

func (s *GhosttyState) RenderFrame(full bool) ([]byte, error) {
	return s.frame(full, true)
}

func (s *GhosttyState) SnapshotFrame() ([]byte, error) {
	return s.frame(true, false)
}

func (s *GhosttyState) frame(full, clear bool) ([]byte, error) {
	var size C.size_t
	var noChange C.bool
	data := C.frame_json(s.vt, C.bool(full), C.bool(clear), &size, &noChange)
	if data == nil {
		if bool(noChange) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("render Ghostty terminal frame")
	}
	defer C.free(unsafe.Pointer(data))
	if size > 8*1024*1024 {
		return nil, fmt.Errorf("Ghostty screen frame exceeds 8 MiB")
	}
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), nil
}

func (s *GhosttyState) Scroll(rows int) (bool, error) {
	if rows == 0 {
		return false, nil
	}
	C.scroll_vt(s.vt, C.int32_t(rows))
	return true, nil
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
