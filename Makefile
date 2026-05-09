# api-test Makefile
#
# Common targets:
#   make build        # build the binary into ./bin/api-test
#   make test         # go test -race -count=1
#   make verify       # full CI-equivalent: tools-check, fmt, vet, test, lint, security, coverage
#   make dev-anon     # postgres-free anonymous-mode binary; fastest iteration
#
# Run `make help` to see every target.

SHELL := /bin/bash

BINARY_NAME := api-test

VERSION    ?= $(shell \
    tag=$$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0); \
    git diff --quiet HEAD -- 2>/dev/null && echo $$tag || echo $$tag-dirty)
GIT_SHA    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X github.com/plexara/api-test/pkg/build.Version=$(VERSION) \
                     -X github.com/plexara/api-test/pkg/build.Commit=$(GIT_SHA) \
                     -X github.com/plexara/api-test/pkg/build.Date=$(BUILD_DATE)"

CMD_DIR      := ./cmd/api-test
BUILD_DIR    := ./bin
UI_DIR       := ./ui
UI_EMBED_DIR := ./internal/ui/dist

# Pinned tool versions; keep in sync with .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION := v2.11.4
GOSEC_VERSION         := v2.25.0

TOOLS_DIR := $(abspath $(BUILD_DIR)/tools)

GO       := go
GOTEST   := $(GO) test
GOBUILD  := $(GO) build
GOMOD    := $(GO) mod
GOFMT    := gofmt
GOLINT   := $(TOOLS_DIR)/golangci-lint
GOSEC    := $(TOOLS_DIR)/gosec
GOVULN   := $(TOOLS_DIR)/govulncheck

.PHONY: all build test test-short bench fmt fmt-check vet tidy clean help dev-secrets \
        ui ui-dev ui-clean embed-clean \
        lint security gosec govulncheck \
        coverage coverage-gate coverage-report \
        verify tools-check tools-install \
        dev dev-anon dev-up dev-wait dev-ui-if-needed dev-down dev-logs \
        docker docs docs-serve run version

## all: Build, test, lint
all: build test lint

## build: Build the binary into ./bin/api-test
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Binary built: $(BUILD_DIR)/$(BINARY_NAME)"

## test: Run unit tests with race detector
test:
	@echo "Running tests..."
	$(GOTEST) -race -count=1 ./...

## test-short: Skip integration / long tests (-short)
test-short:
	$(GOTEST) -short -count=1 ./...

## bench: Run benchmarks
bench:
	$(GOTEST) -run=^$$ -bench=. -benchmem ./...

## fmt: Apply gofmt -s
fmt:
	@echo "Running gofmt..."
	$(GOFMT) -s -w .

## fmt-check: Fail if gofmt would change anything
fmt-check:
	@echo "Checking gofmt..."
	@out="$$($(GOFMT) -s -l .)"; \
	if [ -n "$$out" ]; then \
		echo "FAIL: files need 'make fmt':"; echo "$$out"; exit 1; \
	fi
	@echo "gofmt clean."

## vet: go vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./...

## tidy: go mod tidy
tidy:
	$(GOMOD) tidy

## lint: golangci-lint run (pinned version from $(TOOLS_DIR))
lint: tools-check
	@echo "Running golangci-lint $(GOLANGCI_LINT_VERSION)..."
	$(GOLINT) run --timeout=5m

## gosec: Static security analyzer (pinned version from $(TOOLS_DIR))
gosec: tools-check
	@echo "Running gosec $(GOSEC_VERSION)..."
	$(GOSEC) -quiet ./...

## govulncheck: Known-vulnerability scan
govulncheck: tools-check
	@echo "Running govulncheck..."
	$(GOVULN) ./...

## security: gosec + govulncheck
security: gosec govulncheck

COVERAGE_MIN ?= 80

## coverage: Run tests and produce a coverage profile.
coverage:
	@echo "Running coverage..."
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	@$(GO) tool cover -func=coverage.out | tail -1

## coverage-gate: Fail if coverage of testable packages is below COVERAGE_MIN (default 80)
##                 Excludes Postgres-dependent packages (apikeys, audit/postgres,
##                 database, database/migrate) — those are covered by the
##                 integration test suite (go test -tags integration) which
##                 doesn't contribute to the unit-test coverage profile.
##                 Also excludes cmd/api-test (binary entry; tested manually).
COVERAGE_EXCLUDE := cmd/api-test|pkg/apikeys|pkg/audit/postgres|pkg/database
coverage-gate: coverage
	@total=$$( \
		$(GO) tool cover -func=coverage.out \
		| grep -Ev "$(COVERAGE_EXCLUDE)" \
		| awk '$$3 ~ /%$$/ {gsub(/%/,"",$$3); sum+=$$3; n++} END { if (n==0) { print 0 } else { printf "%.1f", sum/n } }' \
	); \
	awk -v total=$$total -v min=$(COVERAGE_MIN) 'BEGIN { if (total+0 < min+0) { printf "coverage (testable subset) %s%% < %s%%\n", total, min; exit 1 } else { printf "coverage (testable subset) %s%% >= %s%%\n", total, min } }'

## tools-install: Install lint/security tools at the pinned versions into $(TOOLS_DIR).
TOOLS_STAMP := $(TOOLS_DIR)/.installed-$(GOLANGCI_LINT_VERSION)-$(GOSEC_VERSION)
tools-install: $(TOOLS_STAMP)

$(TOOLS_STAMP):
	@echo "Installing pinned tools into $(TOOLS_DIR)..."
	@mkdir -p $(TOOLS_DIR)
	@rm -f $(TOOLS_DIR)/.installed-* 2>/dev/null || true
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	GOBIN=$(TOOLS_DIR) $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	@touch $@

## tools-check: Verify pinned tools are present at the right versions; auto-installs.
tools-check: tools-install
	@echo "Tools pinned at $(TOOLS_DIR):"
	@echo "  golangci-lint: $$($(GOLINT) --version 2>/dev/null | head -1)"
	@echo "  gosec:         $$($(GOSEC) --version 2>/dev/null | head -1)"
	@echo "  govulncheck:   $$(test -x $(GOVULN) && echo present || echo MISSING)"

## verify: Full CI-equivalent suite. Fails on any error including <80% coverage.
verify: tools-check fmt-check vet test lint security coverage-gate
	@echo ""
	@echo "=== verify: all checks passed ==="
	@# Pre-commit gate sentinel: record the current diff hash so the
	@# review-gate hook knows verify is green for this exact tree state.
	@mkdir -p .claude
	@{ git diff --cached HEAD 2>/dev/null; git diff 2>/dev/null; } \
		| shasum -a 256 | cut -c1-16 > .claude/.last-verify-passed

## dev-anon: Run anonymous-mode dev binary; no DB, no auth (M1 happy path).
dev-anon:
	$(GO) run $(LDFLAGS) $(CMD_DIR) --config configs/api-test.dev.yaml

## dev-secrets: Generate .env.dev with random cookie secret + dev API key on first run.
dev-secrets:
	@if [ ! -f .env.dev ]; then \
		echo "Generating .env.dev with random secrets (gitignored)..."; \
		printf 'export APITEST_COOKIE_SECRET=%s\nexport APITEST_DEV_KEY=%s\nexport APITEST_DEV_BEARER=%s\n' \
			"$$(head -c 48 /dev/urandom | base64 | tr -d '\n')" \
			"apitest_$$(head -c 24 /dev/urandom | base64 | tr -d '\n=+/' | head -c 32)" \
			"apitest_bearer_$$(head -c 24 /dev/urandom | base64 | tr -d '\n=+/' | head -c 32)" \
			> .env.dev; \
		chmod 600 .env.dev; \
	fi

## dev: Full local stack (M3+). For now, points at dev-anon.
##      M3 will replace with: postgres + keycloak in compose, binary in foreground.
dev: dev-anon

## run: Build and run with dev config
run: build
	$(BUILD_DIR)/$(BINARY_NAME) --config configs/api-test.dev.yaml

## docker: Build the docker image (M5: matches goreleaser pipeline).
docker: build
	@mkdir -p linux/amd64
	@cp $(BUILD_DIR)/$(BINARY_NAME) linux/amd64/$(BINARY_NAME)
	docker buildx build --platform linux/amd64 \
		--build-arg TARGETARCH=amd64 \
		-t $(BINARY_NAME):$(VERSION) \
		--load .
	@rm -rf linux/

## docs: Build the documentation site (M5; requires mkdocs-material).
docs:
	mkdocs build --strict

## docs-serve: Serve the documentation site locally (M5).
DOCS_HOST ?= 127.0.0.1
DOCS_PORT ?= 8001
docs-serve:
	mkdocs serve -a $(DOCS_HOST):$(DOCS_PORT)

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

## version: Show resolved version metadata
version:
	@echo "Binary:     $(BINARY_NAME)"
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(GIT_SHA)"
	@echo "Build date: $(BUILD_DATE)"
	@echo "Go:         $$($(GO) version | cut -d ' ' -f 3)"

## help: Show this help
help:
	@echo "$(BINARY_NAME) Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
