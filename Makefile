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

.PHONY: build plugins desktop package install uninstall desktop-check integration test lint check doctor install-spec

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

desktop: $(FRONTEND)/node_modules/.package-lock.json
	@npm --prefix $(FRONTEND) run build
	@$(GO) build -tags 'desktop production' -ldflags '-extldflags "-framework UniformTypeIdentifiers"' -trimpath -o "$(BIN)/Flows-desktop" ./desktop/app

.PHONY: package-daemon
package-daemon: libghostty
	@CGO_CFLAGS='-I "$(GHOSTTY_DIR)/include"' CGO_LDFLAGS='"$(GHOSTTY_DIR)/lib/libghostty-vt.a"' $(GO) build -tags ghostty -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=$(VERSION)" -o "$(BIN)/flowsd-release" ./cmd/flowsd

package:
	@VERSION="$(VERSION)" sh build/package-macos.sh

install: package
	@mkdir -p "$${HOME}/Applications" "$${HOME}/.local/bin"
	@rm -rf "$${HOME}/Applications/Flows.app"
	@cp -R build/dist/Flows.app "$${HOME}/Applications/Flows.app"
	@ln -sf "$${HOME}/Applications/Flows.app/Contents/Resources/bin/flows" "$${HOME}/.local/bin/flows"
	@$(MAKE) install-spec
	@env -u FLOWS_HOME "$${HOME}/Applications/Flows.app/Contents/MacOS/flowsd" register
	@env -u FLOWS_HOME "$${HOME}/.local/bin/flows" status

uninstall:
	@if launchctl print "gui/$$(id -u)/dev.flows.daemon" >/dev/null 2>&1; then launchctl bootout "gui/$$(id -u)/dev.flows.daemon"; fi
	@rm -f "$${HOME}/Library/LaunchAgents/dev.flows.daemon.plist"
	@rm -rf "$${HOME}/Applications/Flows.app" "$${HOME}/.local/bin/flows"

desktop-check: desktop
	@npm --prefix $(FRONTEND) run check
	@npm --prefix $(FRONTEND) test
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
