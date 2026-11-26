include build/toolchain.lock

GO ?= go
VERSION ?= dev
BIN := build/bin
TOOLS := build/tools
STATICCHECK := $(abspath $(TOOLS)/$(STATICCHECK_VERSION)/staticcheck)
MODULES := . sdk/go
COMMANDS := flows flowsd flows-host
export GOTOOLCHAIN := $(addprefix go,$(GO_VERSION))
export MACOSX_DEPLOYMENT_TARGET := $(MACOS_DEPLOYMENT_TARGET)

.PHONY: build test lint check doctor

build:
	@mkdir -p "$(BIN)"
	@set -e; for name in $(COMMANDS); do \
		$(GO) build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=$(VERSION)" -o "$(BIN)/$$name" "./cmd/$$name"; \
	done

test:
	@set -e; for module in $(MODULES); do \
		(cd "$$module" && $(GO) test ./...); \
	done

$(STATICCHECK): build/toolchain.lock
	GOBIN="$(dir $(STATICCHECK))" $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

lint: $(STATICCHECK)
	@set -e; for module in $(MODULES); do \
		(cd "$$module" && $(GO) vet ./... && "$(STATICCHECK)" ./...); \
	done

check: test lint

doctor:
	@sh scripts/doctor.sh "$(GO)" "$(STATICCHECK)"
