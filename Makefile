# SPDX-License-Identifier: GPL-3.0-or-later
SHELL := bash
.DEFAULT_GOAL := help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/joernott/dcdash/internal/version
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)

BIN       := bin/dcdash
UI_DIST   := internal/ui/dist
DEV_DIR   := .dev
DEV_HOST  ?= localhost

GO_VERSION   := $(shell cat .go-version)
NODE_VERSION := $(shell cat .node-version)
LINT_VERSION := $(shell cat .golangci-lint-version)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: all
all: lint test build ## Lint, test and build everything

## --- Build -----------------------------------------------------------------

.PHONY: build
build: build-web build-go ## Build frontend and binary (bin/dcdash)

.PHONY: build-go
build-go: ## Build the Go binary with the currently embedded frontend
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/dcdash

.PHONY: build-web
build-web: web/node_modules ## Build the frontend into internal/ui/dist
	cd web && npm run build

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci --no-fund --no-audit
	@touch $@

## --- Quality ---------------------------------------------------------------

.PHONY: test
test: test-go test-web ## Run all unit tests

.PHONY: test-go
test-go: ## Run Go tests with the race detector
	go test -race ./...

.PHONY: test-integration
test-integration: ## Run integration tests against containers (needs Docker; KEEP=1 keeps them)
	scripts/integration-test.sh

.PHONY: test-web
test-web: web/node_modules ## Run frontend unit tests
	cd web && npm test

.PHONY: lint
lint: lint-go lint-web ## Run all linters (no auto-fix)

.PHONY: lint-go
lint-go: ## Run golangci-lint
	golangci-lint run

.PHONY: lint-web
lint-web: web/node_modules ## Lint, format-check and type-check the frontend
	cd web && npm run check && npm run type-check

.PHONY: gen-api
gen-api: web/node_modules ## Regenerate web/src/api/schema.d.ts from api/openapi.yaml
	cd web && npm run gen:api

.PHONY: fmt
fmt: web/node_modules ## Format Go and frontend sources
	golangci-lint fmt
	cd web && npm run format && npm run lint

.PHONY: tools-check
tools-check: ## Verify that the pinned tool versions are installed
	@go version | grep -q "go$(GO_VERSION) " || { echo "expected Go $(GO_VERSION), got: $$(go version)"; exit 1; }
	@node --version | grep -qx "v$(NODE_VERSION)" || { echo "expected Node v$(NODE_VERSION), got: $$(node --version)"; exit 1; }
	@golangci-lint version | grep -q "$(LINT_VERSION:v%=%)" || { echo "expected golangci-lint $(LINT_VERSION)"; exit 1; }
	@echo "tool versions OK"

## --- Development -----------------------------------------------------------

$(DEV_DIR)/tls/cert.pem:
	mkdir -p $(DEV_DIR)/tls $(DEV_DIR)/data
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 365 \
		-subj "/CN=$(DEV_HOST)" \
		-addext "subjectAltName=DNS:$(DEV_HOST),DNS:localhost,IP:127.0.0.1" \
		-keyout $(DEV_DIR)/tls/key.pem -out $@
	chmod 600 $(DEV_DIR)/tls/key.pem

.PHONY: dev-cert
dev-cert: $(DEV_DIR)/tls/cert.pem ## Create a self-signed development certificate in .dev/tls

.PHONY: dev-backend
dev-backend: dev-cert ## Run the backend with configs/dev.yaml (https://localhost:8443)
	go run ./cmd/dcdash --config configs/dev.yaml serve

.PHONY: dev-web
dev-web: web/node_modules ## Run the Vite dev server with proxy to the backend (DEV_HOST=0.0.0.0 to expose it)
	cd web && npm run dev -- --host $(DEV_HOST)

.PHONY: clean
clean: ## Remove build output
	rm -rf bin dist
	find $(UI_DIST) -mindepth 1 ! -name .gitkeep -delete
