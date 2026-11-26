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
