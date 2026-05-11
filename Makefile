# api-test Makefile
#
# `make verify` is the single source of truth for "is this tree shippable".
# It runs every check the CI workflows run — same commands, same versions,
# same outcome. If CI catches something this target doesn't, that's a bug
# in this Makefile, not in the code. The verify-passed sentinel
# (.claude/.last-verify-passed) is only written after the FULL set passes.
#
# Common targets:
#   make build        # compile the binary into ./bin/api-test
#   make verify       # full CI-equivalent gate (slow; pre-commit / pre-push)
#   make test         # unit tests with race detector (inner-loop iteration)
#   make dev-anon     # postgres-free anonymous-mode binary; fastest dev loop
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
SEMGREP_VERSION       := 1.110.0

TOOLS_DIR := $(abspath $(BUILD_DIR)/tools)

GO       := go
GOTEST   := $(GO) test
GOBUILD  := $(GO) build
GOMOD    := $(GO) mod
GOFMT    := gofmt
GOLINT   := $(TOOLS_DIR)/golangci-lint
GOSEC    := $(TOOLS_DIR)/gosec
GOVULN   := $(TOOLS_DIR)/govulncheck

# CodeQL artifacts. Re-created on each `make codeql` run.
CODEQL_DB     := $(BUILD_DIR)/codeql-db
CODEQL_RESULT := $(BUILD_DIR)/codeql-results.sarif

.PHONY: all build build-all test test-short bench fmt fmt-check vet tidy \
        mod-tidy-check mod-verify clean help dev-secrets \
        ui ui-dev ui-clean ui-verify check-ui-embed embed-clean \
        lint security gosec govulncheck semgrep \
        coverage coverage-gate coverage-report \
        integration codeql require-docker require-codeql require-semgrep require-jq require-node \
        verify tools-check tools-install \
        dev dev-anon dev-up dev-wait dev-ui-if-needed dev-down dev-logs \
        docker docs docs-serve screenshots run version

## all: Build, test, lint
all: build test lint

## build: Build the binary into ./bin/api-test
##        Refuses to build when the SPA embed dir is empty — the Go
##        //go:embed directive would silently produce a binary that
##        serves a JSON stub instead of the portal. Run `make ui` first
##        (or `make all` which chains them in order).
build: check-ui-embed
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Binary built: $(BUILD_DIR)/$(BINARY_NAME)"

## check-ui-embed: Verify $(UI_EMBED_DIR) has a built SPA. Used as a
##                 build-time prerequisite so a freshly cloned tree
##                 (only $(UI_EMBED_DIR)/.gitkeep present) doesn't
##                 produce a UI-less binary by accident.
check-ui-embed:
	@if [ ! -f $(UI_EMBED_DIR)/index.html ]; then \
		echo ""; \
		echo "FAIL: $(UI_EMBED_DIR)/index.html missing."; \
		echo "      The Go binary embeds $(UI_EMBED_DIR) via //go:embed."; \
		echo "      Without a built SPA there, the portal serves a JSON"; \
		echo "      stub instead of the React UI. Run:"; \
		echo ""; \
		echo "          make ui"; \
		echo ""; \
		echo "      and re-run `make build`."; \
		exit 1; \
	fi

## build-all: go build -v ./... (mirrors CI's Build job; catches build paths
##            that `go test` would skip — packages without tests, etc.)
build-all:
	@echo "Building all packages..."
	$(GOBUILD) -v ./...

## ui: Build the SPA into internal/ui/dist for embedding into the binary.
##     Uses pnpm with --frozen-lockfile so reproducible.
ui: require-node
	@echo "Building UI..."
	cd $(UI_DIR) && pnpm install --frozen-lockfile && pnpm build
	@rm -rf $(UI_EMBED_DIR)
	@cp -R $(UI_DIR)/dist $(UI_EMBED_DIR)
	@# Re-add the .gitkeep so the directory stays tracked in git even
	@# though the bundle itself is gitignored. Fresh clones rely on the
	@# .gitkeep so the //go:embed directive in internal/ui/embed.go has
	@# a directory to point at before `make ui` runs.
	@touch $(UI_EMBED_DIR)/.gitkeep
	@echo "UI built and copied to $(UI_EMBED_DIR)."

## ui-verify: TypeScript + Vite build of the SPA, then fail if the
##            embedded copy in $(UI_EMBED_DIR) does not match the fresh
##            build under $(UI_DIR)/dist. The Go binary embeds the latter
##            via //go:embed, so a stale embed silently ships old UI
##            even when source is current. This gate makes that failure
##            mode loud (same posture as fmt-check / mod-tidy-check).
##            To fix a failure: run `make ui` and commit $(UI_EMBED_DIR).
ui-verify: require-node
	@echo "Verifying UI (typecheck + build)..."
	cd $(UI_DIR) && pnpm install --frozen-lockfile && pnpm build
	@echo "Checking embedded SPA bundle is in sync with fresh build..."
	@# .gitkeep lives in the embed dir only (to keep the gitignored
	@# directory tracked); exclude it from the comparison.
	@if ! diff -r --brief --exclude=.gitkeep $(UI_DIR)/dist $(UI_EMBED_DIR) > /dev/null 2>&1; then \
		echo ""; \
		echo "FAIL: $(UI_EMBED_DIR) is stale relative to $(UI_DIR)/dist."; \
		echo "      The Go binary embeds $(UI_EMBED_DIR); without refreshing"; \
		echo "      it, the binary ships an outdated SPA even when source is"; \
		echo "      current. Run:"; \
		echo ""; \
		echo "          make ui"; \
		echo "          git add $(UI_EMBED_DIR)"; \
		echo ""; \
		echo "      and commit before re-running verify."; \
		exit 1; \
	fi
	@echo "Embedded SPA bundle is in sync."

## ui-dev: Run Vite dev server (proxies /api to localhost:8080).
ui-dev:
	cd $(UI_DIR) && pnpm dev

## ui-clean: Remove UI build artifacts (dist + node_modules).
ui-clean:
	@rm -rf $(UI_DIR)/dist $(UI_DIR)/node_modules

## embed-clean: Reset internal/ui/dist to .gitkeep only (matches a clean
##              CI checkout; useful before `make ui` to confirm the build
##              produces a complete dist tree from scratch).
embed-clean:
	@echo "Cleaning UI embed directory..."
	@rm -rf $(UI_EMBED_DIR)
	@mkdir -p $(UI_EMBED_DIR)
	@touch $(UI_EMBED_DIR)/.gitkeep

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

## mod-tidy-check: Fail if `go mod tidy` would change go.mod / go.sum.
##                 Snapshots the files first so an already-dirty working
##                 tree (uncommitted edits to go.mod for a separate task)
##                 doesn't false-positive. Restores on either outcome.
##                 Mirrors CI's "Verify go.mod is tidy" step in spirit;
##                 CI runs in a fresh checkout so it doesn't need the
##                 snapshot, but local does.
mod-tidy-check:
	@echo "Checking go.mod is tidy..."
	@# Single chained shell: trap installed first so it covers every
	@# subsequent command (including the cp calls) — leak-free even on
	@# Ctrl-C between snapshots. `set -e` so any failure (cp, tidy,
	@# diff) hard-aborts the recipe; without it, `;` between commands
	@# silently swallows non-zero exits and the gate becomes a lie.
	@trap 'mv -f go.mod.tidy-snapshot go.mod 2>/dev/null; mv -f go.sum.tidy-snapshot go.sum 2>/dev/null; true' EXIT; \
	set -e; \
	cp go.mod go.mod.tidy-snapshot; \
	cp go.sum go.sum.tidy-snapshot; \
	$(GOMOD) tidy; \
	if ! diff -q go.mod go.mod.tidy-snapshot >/dev/null || ! diff -q go.sum go.sum.tidy-snapshot >/dev/null; then \
		echo "FAIL: go.mod / go.sum are out of date; run 'go mod tidy' locally" >&2; \
		diff -u go.mod.tidy-snapshot go.mod || true; \
		diff -u go.sum.tidy-snapshot go.sum || true; \
		exit 1; \
	fi
	@echo "go.mod / go.sum: tidy."

## mod-verify: go mod verify (mirrors CI's Build job step)
mod-verify:
	@echo "Verifying module checksums..."
	$(GOMOD) verify

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

## semgrep: Run Semgrep with the same configs CI uses (p/golang + .semgrep/).
##          Hard-fails if the semgrep CLI is not installed — silent skip
##          would let CI catch what local missed. Warns if the local
##          version doesn't match the pinned $(SEMGREP_VERSION) so
##          rule-set drift between local and CI is visible.
semgrep: require-semgrep
	@actual="$$(semgrep --version 2>&1 | head -1)"; \
	if [ "$$actual" != "$(SEMGREP_VERSION)" ]; then \
		echo "WARN: semgrep version drift — pinned: $(SEMGREP_VERSION), local: $$actual" >&2; \
		echo "      install matching: pipx install --force semgrep==$(SEMGREP_VERSION)" >&2; \
	fi
	@echo "Running semgrep $(SEMGREP_VERSION) (p/golang + .semgrep/)..."
	semgrep scan --error --quiet --config p/golang --config .semgrep/

## security: gosec + govulncheck + semgrep (mirrors CI's Security job)
security: gosec govulncheck semgrep

COVERAGE_MIN ?= 80

## coverage: Run tests and produce a coverage profile.
coverage:
	@echo "Running coverage..."
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	@$(GO) tool cover -func=coverage.out | tail -1

## coverage-gate: Fail if coverage of testable packages is below COVERAGE_MIN (default 80)
##                 Delegates to scripts/coverage-gate.sh so CI and local
##                 use identical logic. The script excludes Postgres-
##                 dependent packages (apikeys, audit/postgres,
##                 database, database/migrate) and cmd/api-test from the
##                 gate; those are covered by the integration suite.
coverage-gate: coverage
	@./scripts/coverage-gate.sh coverage.out $(COVERAGE_MIN)

## integration: Run the integration test suite under build tag `integration`
##              against testcontainers Postgres. Mirrors CI's Integration job.
##              Hard-fails if Docker is not available — testcontainers needs it.
integration: require-docker
	@echo "Running integration tests (testcontainers Postgres)..."
	$(GOTEST) -tags=integration -race -count=1 -timeout=10m ./tests/...

## codeql: Run the same CodeQL security-and-quality suite CI runs.
##         Builds the database from source, runs the analysis, and
##         filters the SARIF against .github/codeql/codeql-config.yml
##         exclusions. Hard-fails if the codeql or jq CLIs are not on
##         PATH (jq is used by scripts/codeql-gate.sh to parse SARIF).
##         Slow (~2-3 min on first run, ~1 min cached).
codeql: require-codeql require-jq
	@echo "Building CodeQL database (Go) at $(CODEQL_DB)..."
	@rm -rf $(CODEQL_DB)
	@mkdir -p $(BUILD_DIR)
	codeql database create $(CODEQL_DB) --language=go --source-root=. --overwrite
	@echo ""
	@echo "Analyzing with security-and-quality + project config..."
	codeql database analyze $(CODEQL_DB) \
		codeql/go-queries:codeql-suites/go-security-and-quality.qls \
		--format=sarif-latest \
		--output=$(CODEQL_RESULT) \
		--threads=0 \
		--sarif-category=/language:go
	@echo ""
	@echo "Filtering against .github/codeql/codeql-config.yml exclusions..."
	@./scripts/codeql-gate.sh $(CODEQL_RESULT) .github/codeql/codeql-config.yml
	@echo "CodeQL: clean."

# --- tool gates: every external tool the verify pipeline depends on must
# either be present or hard-fail with install instructions. Silent skip
# is what got us here.

require-docker:
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "FAIL: docker CLI not found. Install Docker Desktop or colima." >&2; \
		exit 1; \
	fi
	@if ! docker info >/dev/null 2>&1; then \
		echo "FAIL: docker daemon not running. Start Docker Desktop / colima first." >&2; \
		exit 1; \
	fi

require-codeql:
	@if ! command -v codeql >/dev/null 2>&1; then \
		echo "FAIL: codeql CLI not on PATH." >&2; \
		echo "  brew install codeql" >&2; \
		echo "  (or fetch from https://github.com/github/codeql-cli-binaries/releases)" >&2; \
		exit 1; \
	fi

require-semgrep:
	@if ! command -v semgrep >/dev/null 2>&1; then \
		echo "FAIL: semgrep CLI not on PATH." >&2; \
		echo "  pipx install semgrep==$(SEMGREP_VERSION)    # recommended (pinned)" >&2; \
		echo "  pip3 install semgrep==$(SEMGREP_VERSION)    # alternative" >&2; \
		echo "  brew install semgrep                        # macOS (unpinned, may drift)" >&2; \
		exit 1; \
	fi

require-jq:
	@if ! command -v jq >/dev/null 2>&1; then \
		echo "FAIL: jq CLI not on PATH (codeql-gate.sh parses SARIF with it)." >&2; \
		echo "  brew install jq             # macOS" >&2; \
		echo "  apt install jq              # debian/ubuntu" >&2; \
		echo "  dnf install jq              # fedora/rhel" >&2; \
		exit 1; \
	fi

require-node:
	@if ! command -v pnpm >/dev/null 2>&1; then \
		echo "FAIL: pnpm not on PATH (UI build needs it)." >&2; \
		echo "  brew install pnpm           # macOS" >&2; \
		echo "  npm install -g pnpm         # any node install" >&2; \
		exit 1; \
	fi

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

## verify: Full CI-equivalent gate. Runs every check the CI workflows run.
##         Order: cheap → expensive so the loop short-circuits on the first
##         failure as fast as possible. The .claude/.last-verify-passed
##         sentinel is ONLY written after the full set passes; the
##         pre-commit gate hook reads it as the source of truth.
##
##         Mirrors:
##           .github/workflows/ci.yml      (lint, test, build, security, integration)
##           .github/workflows/codeql.yml  (CodeQL security-and-quality)
##
##         External tool requirements (silent skip is forbidden — see
##         require-* targets for install instructions):
##           - docker (running)  for `make integration`
##           - codeql            for `make codeql`
##           - semgrep           for `make semgrep`
verify: tools-check fmt-check mod-tidy-check mod-verify build-all lint security coverage-gate integration codeql ui-verify
	@echo ""
	@echo "=== verify: all checks passed (CI-equivalent set) ==="
	@# Pre-commit gate sentinel: record the current diff hash so the
	@# review-gate hook knows verify is green for this exact tree state.
	@# Only written here, after the FULL set passes. Anything less is a lie.
	@mkdir -p .claude
	@{ git diff --cached HEAD 2>/dev/null; git diff 2>/dev/null; } \
		| shasum -a 256 | cut -c1-16 > .claude/.last-verify-passed

## dev: One-command full local stack — postgres + keycloak in docker,
##      SPA built if missing, binary in foreground against the live config.
##      Generates .env.dev with random secrets on first run (gitignored;
##      subsequent runs reuse so portal sessions persist).
dev: dev-secrets dev-up dev-wait dev-ui-if-needed
	@. ./.env.dev && \
	echo "" && \
	echo "Starting api-test (config: configs/api-test.live.yaml)..." && \
	echo "  Portal:    http://localhost:8080/portal/   (sign in with dev/dev or paste an API key)" && \
	echo "  /v1/*:     http://localhost:8080/v1/...   (X-API-Key: \$$APITEST_DEV_KEY)" && \
	echo "  Keycloak:  http://localhost:8081/         (admin/admin)" && \
	echo "  API key:   $$APITEST_DEV_KEY" && \
	echo "  Bearer:    $$APITEST_DEV_BEARER" && \
	echo "" && \
	$(GO) run $(LDFLAGS) $(CMD_DIR) --config configs/api-test.live.yaml

## dev-anon: Run anonymous-mode dev binary against postgres only — no
##           Keycloak, no auth required, no portal browser login.
##           Fastest iteration for endpoint-group / audit work.
dev-anon: dev-secrets
	@. ./.env.dev && docker compose -f docker-compose.dev.yml up -d postgres
	@. ./.env.dev && $(GO) run $(LDFLAGS) $(CMD_DIR) --config configs/api-test.dev.yaml

## dev-secrets: Generate .env.dev with random cookie secret + dev API key on first run.
##              Re-run-safe; only writes if .env.dev is missing.
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

## dev-up: Start the dev stack (postgres + keycloak) without the binary.
##         Depends on dev-secrets because docker compose interpolates the
##         APITEST_COOKIE_SECRET reference at parse time even when the
##         api-test service isn't being started.
dev-up: dev-secrets require-docker
	@. ./.env.dev && docker compose -f docker-compose.dev.yml up -d postgres keycloak

## dev-wait: Block until postgres and keycloak are reachable.
##           Sources .env.dev because `docker compose exec` re-parses the
##           compose file and its APITEST_* references must resolve.
dev-wait: dev-secrets
	@echo "Waiting for Postgres..."
	@. ./.env.dev && until docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U api >/dev/null 2>&1; do sleep 1; done
	@echo "Waiting for Keycloak realm..."
	@until curl -fs http://localhost:8081/realms/api-test/.well-known/openid-configuration >/dev/null 2>&1; do sleep 2; done
	@echo "Stack ready."

## dev-ui-if-needed: Build the SPA if internal/ui/dist/index.html is missing.
##                   Skipped silently when the embed is already populated.
dev-ui-if-needed:
	@if [ ! -f $(UI_EMBED_DIR)/index.html ]; then \
		$(MAKE) ui; \
	fi

## dev-down: Stop the dev stack and remove the compose network. Volumes
##           (postgres data) are kept; add `-v` to the underlying command
##           to wipe them.
dev-down: dev-secrets
	@. ./.env.dev && docker compose -f docker-compose.dev.yml down

## dev-logs: Tail compose logs (postgres + keycloak + binary if running).
dev-logs: dev-secrets
	@. ./.env.dev && docker compose -f docker-compose.dev.yml logs -f --tail=100

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

## screenshots: Capture portal screenshots (light + dark) for the docs site.
##              Seeds mock audit data via the configured database, then drives
##              Playwright through every portal page. Requires the binary to
##              be running on $(SHOTS_BASE_URL) (default http://localhost:8080).
##              Re-run after any portal UI change.
##
##              Sources .env.dev for APITEST_DEV_KEY so the captured-portal API
##              key matches the running binary's accepted file key. Override
##              SHOTS_API_KEY explicitly to point at a different deployment
##              (e.g. staging); that wins over .env.dev.
SHOTS_BASE_URL ?= http://localhost:8080
screenshots: dev-secrets require-node
	@. ./.env.dev && \
	    KEY="$${SHOTS_API_KEY:-$$APITEST_DEV_KEY}" && \
	    if [ -z "$$KEY" ]; then echo "no API key: set SHOTS_API_KEY or run make dev-secrets"; exit 1; fi && \
	    cd scripts/screenshots && \
	    (test -d node_modules || npm install) && \
	    APITEST_BASE_URL=$(SHOTS_BASE_URL) APITEST_DEV_KEY="$$KEY" node screenshots.mjs

## clean: Remove build artifacts (binary, coverage, codeql db/sarif)
clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html
	@# don't rm $(BUILD_DIR)/tools — pinned linters live there

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
