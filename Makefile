include build/toolchain.lock

GO ?= go
VERSION ?= dev
BIN := build/bin
TOOLS := build/tools
STATICCHECK := $(abspath $(TOOLS)/go$(GO_VERSION)/$(STATICCHECK_VERSION)/staticcheck)
MODULES := . sdk/go
COMMANDS := flows flowsd flows-host
export GOTOOLCHAIN := $(addprefix go,$(GO_VERSION))
export MACOSX_DEPLOYMENT_TARGET := $(MACOS_DEPLOYMENT_TARGET)

.PHONY: clean build plugins desktop package install uninstall desktop-check integration test lint check doctor install-spec

clean:
	@rm -rf "$(BIN)" build/dist "$(FRONTEND)/dist"

build:
	@mkdir -p "$(BIN)"
	@set -e; for name in $(COMMANDS); do \
		$(GO) build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=$(VERSION)" -o "$(BIN)/$$name" "./cmd/$$name"; \
	done

plugins:
	@mkdir -p "$(BIN)/plugins"
	@set -e; for p in git github gitlab jira youtrack zulip; do \
		$(GO) build -trimpath -o "$(BIN)/plugins/$$p-plugin" "./plugins/$$p/cmd/plugin"; \
	done

FRONTEND := desktop/app/frontend
$(FRONTEND)/node_modules/.package-lock.json: $(FRONTEND)/package-lock.json $(FRONTEND)/package.json
	@npm --prefix $(FRONTEND) ci --no-audit --no-fund

desktop: desktop-fonts libghostty $(FRONTEND)/node_modules/.package-lock.json
	@npm --prefix $(FRONTEND) run build
	@CGO_CFLAGS='-I "$(GHOSTTY_DIR)/include"' CGO_LDFLAGS='"$(GHOSTTY_SURFACE_ARCHIVE)" -framework AppKit -framework Carbon -framework CoreText -framework IOSurface -framework Metal -framework QuartzCore -framework UniformTypeIdentifiers -lc++' $(GO) build -tags 'desktop production' -trimpath -o "$(BIN)/Flows-desktop" ./desktop/app

.PHONY: package-daemon
package-daemon: libghostty
	@CGO_CFLAGS='-I "$(GHOSTTY_DIR)/include"' CGO_LDFLAGS='"$(GHOSTTY_DIR)/lib/libghostty-vt.a"' $(GO) build -tags ghostty -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=$(VERSION)" -o "$(BIN)/flowsd-release" ./cmd/flowsd

package:
	@VERSION="$(VERSION)" sh build/package-macos.sh

install:
	@$(MAKE) clean
	@$(MAKE) package
	@mkdir -p "$${HOME}/Applications" "$${HOME}/.local/bin"
	@rm -rf "$${HOME}/Applications/Flows.app"
	@cp -R build/dist/Flows.app "$${HOME}/Applications/Flows.app"
	@ln -sf "$${HOME}/Applications/Flows.app/Contents/Resources/bin/flows" "$${HOME}/.local/bin/flows"
	@$(MAKE) install-spec
	@env -u FLOWS_HOME "$${HOME}/.local/bin/flows" install-bundled
	@env -u FLOWS_HOME "$${HOME}/Applications/Flows.app/Contents/MacOS/flowsd" register --restart
	@env -u FLOWS_HOME "$${HOME}/.local/bin/flows" status

uninstall:
	@if launchctl print "gui/$$(id -u)/dev.flows.daemon" >/dev/null 2>&1; then launchctl bootout "gui/$$(id -u)/dev.flows.daemon"; fi
	@rm -f "$${HOME}/Library/LaunchAgents/dev.flows.daemon.plist"
	@rm -rf "$${HOME}/Applications/Flows.app" "$${HOME}/.local/bin/flows"

.PHONY: ui-check
ui-check: desktop-fonts $(FRONTEND)/node_modules/.package-lock.json
	@npm --prefix $(FRONTEND) run build
	@npm --prefix $(FRONTEND) run test:ui

desktop-check: desktop
	@npm --prefix $(FRONTEND) run check
	@npm --prefix $(FRONTEND) test
	@npm --prefix $(FRONTEND) run test:ui
	@$(GO) test ./desktop/...

integration:
	@$(GO) test ./integration/...

test:
	@set -e; for module in $(MODULES); do \
		(cd "$$module" && $(GO) test ./...); \
	done

$(STATICCHECK):
	GOBIN="$(dir $(STATICCHECK))" $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

lint: $(STATICCHECK)
	@set -e; for module in $(MODULES); do \
		(cd "$$module" && $(GO) vet ./... && "$(STATICCHECK)" ./...); \
	done

check: test lint

doctor:
	@sh scripts/doctor.sh "$(GO)" "$(STATICCHECK)"

install-spec:
	@install -d -m 700 "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows" "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec"
	@mkdir -p "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1"
	@cp -R spec/v1/* "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/"
	@cp schema/workflow-v1.json "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/workflow.schema.json"
	@cp schema/tool-v1.json "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/tool.schema.json"
	@mkdir -p "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/sdk/go"
	@cp -R sdk/go/* "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/sdk/go/"

include build/macos.mk

.PHONY: desktop-fonts
FONTS_VERSION := v3.4.0
FONTS_SHA256 := cad9da572d25e3413f7a15a319d2f3c9e7e915ee016baa99e0d88fc08cf5b781
FONTS_TAR := .deps/fonts/IosevkaTerm-$(FONTS_VERSION).tar.xz
FONTS_DIR := $(FRONTEND)/src/assets/fonts/iosevka-term
FONT_FACES := Regular Bold Italic BoldItalic

desktop-fonts: $(FONTS_DIR)/IosevkaTermNerdFontMono-Regular.ttf

$(FONTS_TAR):
	mkdir -p .deps/fonts
	curl -fsSL -o $@.part https://github.com/ryanoasis/nerd-fonts/releases/download/$(FONTS_VERSION)/IosevkaTerm.tar.xz
	echo "$(FONTS_SHA256)  $@.part" | shasum -a 256 -c -
	mv $@.part $@

$(FONTS_DIR)/IosevkaTermNerdFontMono-Regular.ttf: $(FONTS_TAR)
	mkdir -p $(FONTS_DIR)
	tar -xJf $(FONTS_TAR) -C $(FONTS_DIR) LICENSE.md $(foreach f,$(FONT_FACES),IosevkaTermNerdFontMono-$(f).ttf)
	touch $@
