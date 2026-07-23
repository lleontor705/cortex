.PHONY: build run test test-baseline test-coverage lint fmt clean tidy migrate-up migrate-down docker-build install help

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
	@echo "  run            Run the server"
	@echo "  test           Run all tests"
	@echo "  test-baseline  Validate offline retrieval baseline contracts"
	@echo "  test-coverage  Run tests with coverage report"
	@echo "  lint           Run golangci-lint"
	@echo "  fmt            Format code"
	@echo "  clean          Remove binaries and coverage"
	@echo "  tidy           Run go mod tidy"
	@echo "  migrate-up     Run database migrations"
	@echo "  migrate-down   Rollback database migrations"
	@echo "  docker-build   Build Docker image"
	@echo "  install        Install binary to GOPATH/bin"

# Build the cortex binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(BINARY_PATH) ./cmd/cortex
	@echo "Binary built: $(BINARY_PATH)"

# Run the cortex application
run:
	$(GOCMD) run ./cmd/cortex

# Run tests without coverage
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

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

# Run golangci-lint
lint:
	@echo "Running golangci-lint..."
	$(GOLINT) run ./...

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
	@if [ ! -f "Dockerfile" ]; then \
		echo "Error: Dockerfile not found"; \
		exit 1; \
	fi
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
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
