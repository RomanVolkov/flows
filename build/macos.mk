NATIVE_INPUT_DIGEST := $(shell shasum -a 256 build/toolchain.lock scripts/native-deps.sh scripts/font-family.swift desktop/assets/fonts/OFL.txt | shasum -a 256 | cut -d' ' -f1)
NATIVE_ROOT := $(abspath .deps/$(MACOS_ARCH)/$(NATIVE_INPUT_DIGEST))
GHOSTTY_DIR := $(NATIVE_ROOT)/ghostty
FONTS_DIR := $(NATIVE_ROOT)/fonts
GHOSTTY_CPPFLAGS := -I"$(GHOSTTY_DIR)/include"
GHOSTTY_VT_LDFLAGS := -L"$(GHOSTTY_DIR)/lib" -lghostty-vt
GHOSTTY_SURFACE_ARCHIVE := $(GHOSTTY_DIR)/lib/libghostty-surface.a

.PHONY: deps fonts libghostty
deps: fonts libghostty

fonts:
	@sh scripts/native-deps.sh fonts "$(NATIVE_ROOT)"

libghostty:
	@sh scripts/native-deps.sh ghostty "$(NATIVE_ROOT)"

TERMINAL_CGO := CGO_CFLAGS='-I "$(GHOSTTY_DIR)/include"' CGO_LDFLAGS='-L "$(GHOSTTY_DIR)/lib" -lghostty-vt "-Wl,-rpath,$(GHOSTTY_DIR)/lib"'

.PHONY: terminal-probe
terminal-probe: libghostty
	@mkdir -p "$(BIN)"
	$(TERMINAL_CGO) $(GO) build -tags ghostty -o "$(BIN)/flows-terminal-probe" ./cmd/flows-terminal-probe
	clang -fobjc-arc $(GHOSTTY_CPPFLAGS) -o "$(BIN)/flows-terminal-window" desktop/macos/TerminalView.m desktop/macos/TerminalTests.m "$(GHOSTTY_SURFACE_ARCHIVE)" -framework AppKit -framework Carbon -framework CoreText -framework IOSurface -framework Metal -framework QuartzCore -framework UniformTypeIdentifiers -lc++

.PHONY: terminal-probe-check terminal-probe-bench
terminal-probe-check: terminal-probe
	clang $(GHOSTTY_CPPFLAGS) -o "$(BIN)/flows-vt-check" desktop/macos/SnapshotTests.c $(GHOSTTY_VT_LDFLAGS) -Wl,-rpath,"$(GHOSTTY_DIR)/lib"
	"$(BIN)/flows-vt-check"
	$(TERMINAL_CGO) $(GO) test -tags ghostty ./internal/terminal
	@sh desktop/macos/check-terminal.sh "$(BIN)/flows-terminal-probe" desktop/macos/terminal-fixture.py

terminal-probe-bench: terminal-probe
	@sh desktop/macos/check-terminal.sh --bench "$(BIN)/flows-terminal-probe" desktop/macos/terminal-fixture.py
