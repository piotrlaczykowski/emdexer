# ============================================================
# Emdexer — Root Makefile
# Produces statically-linked binaries for all components.
# CGO_ENABLED=0 ensures maximum portability across Linux distros.
# ============================================================

SHELL           := /bin/bash
PROJECT_ROOT    := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

VERSION         ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT          ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LICENSE_TYPE    ?= Community

GO              := go
GOFLAGS         := CGO_ENABLED=0
LD_VARS         := -X 'github.com/piotrlaczykowski/emdexer/pkg/version.Version=$(VERSION)' \
                   -X 'github.com/piotrlaczykowski/emdexer/pkg/version.Commit=$(COMMIT)' \
                   -X 'github.com/piotrlaczykowski/emdexer/pkg/version.LicenseType=$(LICENSE_TYPE)'
LDFLAGS         := -ldflags="-s -w -extldflags=-static $(LD_VARS)"

BIN_DIR         := $(PROJECT_ROOT)bin

GATEWAY_DIR     := $(PROJECT_ROOT)src/gateway
NODE_DIR        := $(PROJECT_ROOT)src/node
CLI_DIR         := $(PROJECT_ROOT)src/cmd/emdex

GATEWAY_BIN     := $(BIN_DIR)/emdex-gateway
NODE_BIN        := $(BIN_DIR)/emdex-node
CLI_BIN         := $(BIN_DIR)/emdex

.PHONY: all gateway node cli clean fmt vet test help

## all: Build all three binaries (emdex-gateway, emdex-node, emdex CLI)
all: gateway node cli

## gateway: Build emdex-gateway (statically linked, CGO_ENABLED=0)
gateway:
	@echo "[BUILD] emdex-gateway → $(GATEWAY_BIN)"
	@mkdir -p $(BIN_DIR)
	@cd $(GATEWAY_DIR) && \
		$(GOFLAGS) $(GO) build $(LDFLAGS) -o $(GATEWAY_BIN) .
	@echo "[OK]    emdex-gateway built"

## node: Build emdex-node (statically linked, CGO_ENABLED=0)
node:
	@echo "[BUILD] emdex-node → $(NODE_BIN)"
	@mkdir -p $(BIN_DIR)
	@cd $(NODE_DIR) && \
		$(GOFLAGS) $(GO) build $(LDFLAGS) -o $(NODE_BIN) .
	@echo "[OK]    emdex-node built"

## cli: Build emdex management CLI
cli:
	@echo "[BUILD] emdex CLI → $(CLI_BIN)"
	@mkdir -p $(BIN_DIR)
	@cd $(CLI_DIR) && \
		$(GOFLAGS) $(GO) build $(LDFLAGS) -o $(CLI_BIN) .
	@echo "[OK]    emdex CLI built"

## clean: Remove all built binaries
clean:
	@echo "[CLEAN] Removing $(BIN_DIR)"
	@rm -rf $(BIN_DIR)

## fmt: Run gofmt on all modules
fmt:
	@echo "[FMT] gateway"
	@cd $(GATEWAY_DIR) && $(GO) fmt ./...
	@echo "[FMT] node"
	@cd $(NODE_DIR) && $(GO) fmt ./...
	@echo "[FMT] cli"
	@cd $(CLI_DIR) && $(GO) fmt ./...

## vet: Run go vet on all modules
vet:
	@echo "[VET] gateway"
	@cd $(GATEWAY_DIR) && $(GO) vet ./...
	@echo "[VET] node"
	@cd $(NODE_DIR) && $(GO) vet ./...
	@echo "[VET] cli"
	@cd $(CLI_DIR) && $(GO) vet ./...

## test: Run tests across all modules
test:
	@echo "[TEST] pkg"
	@cd src/pkg && $(GOFLAGS) $(GO) test ./...
	@echo "[TEST] gateway"
	@cd $(GATEWAY_DIR) && $(GOFLAGS) $(GO) test ./...
	@echo "[TEST] node"
	@cd $(NODE_DIR) && $(GOFLAGS) $(GO) test ./...
	@echo "[TEST] cli"
	@cd $(CLI_DIR) && $(GOFLAGS) $(GO) test ./...
	@echo "[TEST] integration"
	@cd src/tests/integration && $(GOFLAGS) $(GO) test -v ./...
	@echo "[TEST] cli-shell"
	@chmod +x ./src/tests/cli/cli_test.sh && ./src/tests/cli/cli_test.sh

## help: Show this help message
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
