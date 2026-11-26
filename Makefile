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

desktop:
	@clang -fsyntax-only -fobjc-arc desktop/macos/WorkspaceView.m desktop/macos/CanvasView.m desktop/macos/CanvasGestures.m

package:
	@VERSION="$(VERSION)" sh build/package-macos.sh

install: package
	@mkdir -p "$${HOME}/Applications" "$${HOME}/.local/bin"
	@rm -rf "$${HOME}/Applications/Flows.app"
	@cp -R build/dist/Flows.app "$${HOME}/Applications/Flows.app"
	@ln -sf "$${HOME}/Applications/Flows.app/Contents/MacOS/flows" "$${HOME}/.local/bin/flows"
	@$(MAKE) install-spec

uninstall:
	@rm -rf "$${HOME}/Applications/Flows.app" "$${HOME}/.local/bin/flows"

desktop-check:
	@$(GO) test ./desktop/...
	@clang -fsyntax-only -fobjc-arc desktop/macos/WorkspaceView.m desktop/macos/CanvasView.m desktop/macos/CanvasGestures.m
	@clang -fobjc-arc -framework AppKit desktop/macos/WorkspaceTests.m -o "$(BIN)/WorkspaceTests"
	@"$(BIN)/WorkspaceTests"

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
	@mkdir -p "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1"
	@cp -R spec/v1/* "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/"
	@cp schema/workflow-v1.json "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/workflow.schema.json"
	@cp schema/tool-v1.json "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/tool.schema.json"
	@mkdir -p "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/sdk/go"
	@cp -R sdk/go/* "$${XDG_CONFIG_HOME:-$$HOME/.config}/flows/spec/v1/sdk/go/"

include build/macos.mk
