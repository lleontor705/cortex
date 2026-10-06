.PHONY: build run web-build test test-e2e-docker test-integration test-postgres-integration test-coverage test-web-coverage test-postgres-coverage lint fmt clean tidy migrate-up migrate-down docker-build install docs-build docs-serve help

# Binary name
BINARY_NAME=cortex
BINARY_DIR=bin
BINARY_PATH=$(BINARY_DIR)/$(BINARY_NAME)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Coverage parameters
COVERAGE_DIR=coverage
COVERAGE_FILE=$(COVERAGE_DIR)/coverage.out
COVERAGE_HTML=$(COVERAGE_DIR)/coverage.html

# Migration parameters
MIGRATIONS_DIR=migrations

# Docker parameters
DOCKER_IMAGE=cortex
DOCKER_TAG=latest

# Embedded web parameters
WEB_DIR=web
WEB_OUT=$(WEB_DIR)/out
WEB_DIST=internal/web/dist

# Default target
all: build

# Display help
help:
	@echo "Cortex - Memory Server for AI Coding Assistants"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build          Build the binary"
	@echo "  web-build      Build the Next.js static export and sync it into $(WEB_DIST)"
	@echo "  run            Run the server"
	@echo "  test           Run all tests"
	@echo "  test-e2e-docker Run isolated Docker Compose E2E tests"
	@echo "  test-integration Run generic and PostgreSQL integration tests (requires CORTEX_TEST_POSTGRES_DSN)"
	@echo "  test-postgres-integration Run PostgreSQL integration tests (requires CORTEX_TEST_POSTGRES_DSN)"
	@echo "  test-baseline  Validate offline retrieval baseline contracts"
	@echo "  test-coverage  Run Go tests with coverage report"
	@echo "  test-web-coverage Run web client-core V8 coverage gate"
	@echo "  test-postgres-coverage Run PostgreSQL-backed coverage (requires CORTEX_TEST_POSTGRES_DSN)"
	@echo "  lint           Run golangci-lint"
	@echo "  fmt            Format code"
	@echo "  clean          Remove binaries and coverage"
	@echo "  tidy           Run go mod tidy"
	@echo "  migrate-up     Run database migrations"
	@echo "  migrate-down   Rollback database migrations"
	@echo "  docker-build   Build Docker image"
	@echo "  install        Install binary to GOPATH/bin"
	@echo "  docs-build     Build the MkDocs Material site (mkdocs build --strict)"
	@echo "  docs-serve     Serve the docs site locally with live reload"

# Build the cortex binary (cortex_vectors ships by default so the functional zero-CGO vector path needs no extra flags)
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -tags cortex_vectors -o $(BINARY_PATH) ./cmd/cortex
	@echo "Binary built: $(BINARY_PATH)"

# Build the Next.js static export and sync it into the embedded asset tree.
# npm ci runs only when dependencies are absent. The sync mirrors the fresh
# export into $(WEB_DIST), removing prior export output while preserving the
# committed $(WEB_DIST)/index.html placeholder so go:embed still compiles
# before the first web build. Re-running is idempotent.
web-build:
	@echo "Building web static export..."
	@if [ ! -d "$(WEB_DIR)/node_modules" ]; then npm --prefix $(WEB_DIR) ci; fi
	npm --prefix $(WEB_DIR) run build
	@echo "Syncing $(WEB_OUT) -> $(WEB_DIST)..."
	@mkdir -p $(WEB_DIST)
	@find $(WEB_DIST) -mindepth 1 -maxdepth 1 ! -name index.html -exec rm -rf {} +
	@cp -R $(WEB_OUT)/. $(WEB_DIST)/
	@echo "Precompressing text assets for gzip content negotiation..."
	@find $(WEB_DIST) -type f ! -name '*.gz' \( -name '*.html' -o -name '*.js' -o -name '*.mjs' -o -name '*.css' -o -name '*.json' -o -name '*.txt' -o -name '*.svg' -o -name '*.map' -o -name '*.webmanifest' -o -name '*.xml' \) -exec gzip -k9 {} +
	@echo "Embedded web assets synced into $(WEB_DIST)"

# Run the cortex application
run:
	$(GOCMD) run ./cmd/cortex

# Run tests without coverage
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Run the Docker Compose E2E stack. Requires a working local Docker daemon.
test-e2e-docker:
	$(GOTEST) -v -count=1 -tags docker_e2e ./e2e

# Run the complete explicitly tagged integration suite.
test-integration:
	@echo "Running generic and PostgreSQL integration tests..."
	$(GOTEST) -v -tags "integration postgres_integration" ./...

# Run PostgreSQL integration tests only.
test-postgres-integration:
	@echo "Running PostgreSQL integration tests..."
	$(GOTEST) -v -tags postgres_integration ./internal/store/postgres

# Validate the offline Cortex retrieval baseline contracts.
test-baseline:
	@echo "Validating offline retrieval baseline contracts..."
	$(GOTEST) -v -count=1 ./bench/common -run '^(TestCorpus|TestEvidence|TestF1Score|TestRougeL|TestAggregate|TestRecall|TestMRR|TestNDCG|TestIsolation|TestFilter|TestReport|TestRepro|TestVariance|TestGateRegistry)'
	$(GOTEST) -v -count=1 ./bench/fixtures/cortex-native -run '^Test(Authority|Collision)Fixtures$$'
	$(GOTEST) -v -count=1 ./bench/cortex -run '^(TestRunCurrentProductionBaseline|TestDetect)'
	go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract

# Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report generated: $(COVERAGE_HTML)"
	@$(GOCMD) tool cover -func=$(COVERAGE_FILE)

# Run the mandatory web client-core V8 coverage gate.
test-web-coverage:
	npm --prefix web run test:coverage

# Run whole-project coverage with PostgreSQL integration coverage included.
test-postgres-coverage:
	@echo "Running PostgreSQL-backed coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=$(COVERAGE_FILE) ./...
	@$(GOCMD) tool cover -func=$(COVERAGE_FILE)

# Run golangci-lint with the repo-shared config so local runs match CI exactly
lint:
	@echo "Running golangci-lint..."
	$(GOLINT) run --config .golangci.yml ./...

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .
	@echo "Code formatted"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BINARY_DIR)
	@rm -rf $(COVERAGE_DIR)
	$(GOCLEAN)
	@echo "Clean complete"

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy
	@echo "Dependencies tidied"

# Run database migrations up
migrate-up:
	@echo "Running migrations up..."
	@if [ ! -d "$(MIGRATIONS_DIR)" ]; then \
		echo "Error: Migrations directory not found at $(MIGRATIONS_DIR)"; \
		exit 1; \
	fi
	@$(GOCMD) run ./cmd/cortex migrate up
	@echo "Migrations applied"

# Rollback database migrations
migrate-down:
	@echo "Rolling back migrations..."
	@if [ ! -d "$(MIGRATIONS_DIR)" ]; then \
		echo "Error: Migrations directory not found at $(MIGRATIONS_DIR)"; \
		exit 1; \
	fi
	@$(GOCMD) run ./cmd/cortex migrate down
	@echo "Migrations rolled back"

# Build Docker image
docker-build:
	@echo "Building Docker image..."
	@if [ ! -f "docker/Dockerfile" ]; then \
		echo "Error: docker/Dockerfile not found"; \
		exit 1; \
	fi
	docker build -f docker/Dockerfile -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "Docker image built: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Install binary to GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME) to GOPATH/bin..."
	$(GOCMD) install ./cmd/cortex
	@echo "Installation complete"

# Development targets
dev: build run

# Watch for changes and rebuild (requires: go install github.com/cosmtrek/air@latest)
watch:
	@which air > /dev/null || (echo "Error: air not found. Install with: go install github.com/cosmtrek/air@latest" && exit 1)
	air

# Security scan
security:
	@echo "Running security scan..."
	@govulncheck ./... || echo "govulncheck not installed. Run: go install golang.org/x/vuln/cmd/govulncheck@latest"

# Generate mocks for testing
generate-mocks:
	@echo "Generating mocks..."
	$(GOCMD) generate ./...
	@echo "Mocks generated"

# Documentation site (MkDocs Material). The Python toolchain stays isolated from the
# Go/Node builds. One-time local setup when mkdocs is not already on PATH:
#   python3 -m venv "${TMPDIR:-/tmp/}cortex-docs-venv" \
#     && "${TMPDIR:-/tmp/}cortex-docs-venv/bin/pip" install mkdocs-material \
#     && export PATH="${TMPDIR:-/tmp/}cortex-docs-venv/bin:$PATH"
DOCS_VENV ?= $(patsubst %/,%,$(or $(TMPDIR),/tmp))/cortex-docs-venv
DOCS_MKDOCS ?= mkdocs

docs-build:
	@command -v $(DOCS_MKDOCS) >/dev/null 2>&1 || { \
		echo "$(DOCS_MKDOCS) not found. One-time setup:"; \
		echo "  python3 -m venv $(DOCS_VENV)"; \
		echo "  $(DOCS_VENV)/bin/pip install mkdocs-material"; \
		echo "  export PATH=$(DOCS_VENV)/bin:\$$PATH"; \
		exit 1; \
	}
	$(DOCS_MKDOCS) build --strict

docs-serve:
	@command -v $(DOCS_MKDOCS) >/dev/null 2>&1 || { \
		echo "$(DOCS_MKDOCS) not found. Run 'make docs-build' first to see the setup steps."; \
		exit 1; \
	}
	$(DOCS_MKDOCS) serve
