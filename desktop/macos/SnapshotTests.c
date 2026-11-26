#include <assert.h>
#include <ghostty/vt.h>
#include <string.h>

static void feed(GhosttyTerminal terminal, const char *data) {
    ghostty_terminal_vt_write(terminal, (const uint8_t *)data, strlen(data));
}

static void expect_cursor(GhosttyTerminal terminal, uint16_t x, uint16_t y) {
    uint16_t actual_x = 0, actual_y = 0;
    assert(ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_CURSOR_X, &actual_x) == GHOSTTY_SUCCESS);
    assert(ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_CURSOR_Y, &actual_y) == GHOSTTY_SUCCESS);
    assert(actual_x == x && actual_y == y);
}

int main(void) {
    GhosttyTerminal source = NULL;
    assert(ghostty_terminal_new(NULL, &source, 80, 24) == GHOSTTY_SUCCESS);
    const size_t continuation_limit = 1024;
    assert(ghostty_terminal_set(source, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &continuation_limit) == GHOSTTY_SUCCESS);
    feed(source, "\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[2;3H\x1b[31");

    uint8_t *snapshot = NULL;
    size_t size = 0;
    assert(ghostty_snapshot_encode_alloc(source, NULL, &snapshot, &size) == GHOSTTY_SUCCESS);
    ghostty_terminal_free(source);

    GhosttySnapshotDecoder decoder = NULL;
    assert(ghostty_snapshot_decoder_new_buf(NULL, &decoder, snapshot, size) == GHOSTTY_SUCCESS);
    const bool retain = true;
    assert(ghostty_snapshot_decoder_set(decoder, GHOSTTY_SNAPSHOT_DECODER_OPT_RETAIN_CONTINUATION, &retain) == GHOSTTY_SUCCESS);
    GhosttyTerminal restored = NULL;
    assert(ghostty_snapshot_decoder_decode(decoder, &restored) == GHOSTTY_SUCCESS);
    ghostty_snapshot_decoder_free(decoder);
    ghostty_free(NULL, snapshot, size);

    expect_cursor(restored, 2, 1);
    feed(restored, "m雪");
    expect_cursor(restored, 4, 1);
    GhosttyFormatterTerminalOptions options = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
    options.emit = GHOSTTY_FORMATTER_FORMAT_VT;
    options.extra.size = sizeof(options.extra);
    options.extra.modes = true;
    GhosttyFormatter formatter = NULL;
    assert(ghostty_formatter_terminal_new(NULL, &formatter, restored, options) == GHOSTTY_SUCCESS);
    uint8_t *text = NULL;
    size_t text_size = 0;
    assert(ghostty_formatter_format_alloc(formatter, NULL, &text, &text_size) == GHOSTTY_SUCCESS);
    assert(memmem(text, text_size, "\x1b[?1049h", 8));
    assert(memmem(text, text_size, "\x1b[?2004h", 8));
    assert(memmem(text, text_size, "\x1b[?1000h", 8));
    assert(memmem(text, text_size, "雪", strlen("雪")));
    ghostty_free(NULL, text, text_size);
    ghostty_formatter_free(formatter);
    ghostty_terminal_free(restored);
    return 0;
}
