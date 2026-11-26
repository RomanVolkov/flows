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

.PHONY: build test lint check doctor install-spec

build:
	@mkdir -p "$(BIN)"
	@set -e; for name in $(COMMANDS); do \
		$(GO) build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=$(VERSION)" -o "$(BIN)/$$name" "./cmd/$$name"; \
	done

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
