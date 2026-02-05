# Alancoin Makefile
# Usage: make help

.PHONY: help build test test-unit test-integration lint fmt clean run dev setup deps check

# Default target
.DEFAULT_GOAL := help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofumpt
GOLINT=golangci-lint

# Binary names
BINARY_NAME=alancoin
BINARY_PATH=bin/$(BINARY_NAME)

# Build info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

# Test parameters
COVERAGE_DIR=coverage
COVERAGE_FILE=$(COVERAGE_DIR)/coverage.out
COVERAGE_HTML=$(COVERAGE_DIR)/coverage.html

# Colors for output
GREEN=\033[0;32m
YELLOW=\033[0;33m
RED=\033[0;31m
NC=\033[0m # No Color

##@ General

help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

setup: ## Install dev dependencies (linters, formatters, etc.)
	@echo "$(GREEN)Installing dev dependencies...$(NC)"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install mvdan.cc/gofumpt@latest
	go install github.com/air-verse/air@latest
	@echo "$(GREEN)Done!$(NC)"

deps: ## Download and tidy dependencies
	@echo "$(GREEN)Downloading dependencies...$(NC)"
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "$(GREEN)Done!$(NC)"

dev: ## Run with hot reload (requires air)
	@command -v air > /dev/null 2>&1 || { echo "$(RED)air not installed. Run 'make setup' first$(NC)"; exit 1; }
	air

run: build ## Build and run the server
	@echo "$(GREEN)Starting server...$(NC)"
	./$(BINARY_PATH)

##@ Build

build: ## Build the binary
	@echo "$(GREEN)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p bin
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) ./cmd/server
	@echo "$(GREEN)Built: $(BINARY_PATH)$(NC)"

build-linux: ## Build for Linux (useful for Docker)
	@echo "$(GREEN)Building for Linux...$(NC)"
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH)-linux ./cmd/server

clean: ## Remove build artifacts
	@echo "$(YELLOW)Cleaning...$(NC)"
	rm -rf bin/
	rm -rf $(COVERAGE_DIR)/
	go clean -testcache

##@ Testing

test: test-unit ## Run all tests (alias for test-unit)

test-unit: ## Run unit tests
	@echo "$(GREEN)Running unit tests...$(NC)"
	$(GOTEST) -v -race -short ./...

test-integration: ## Run integration tests (requires testnet)
	@echo "$(GREEN)Running integration tests...$(NC)"
	$(GOTEST) -v -race -run Integration ./test/integration/...

test-coverage: ## Run tests with coverage
	@echo "$(GREEN)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "$(GREEN)Coverage report: $(COVERAGE_HTML)$(NC)"
	@go tool cover -func=$(COVERAGE_FILE) | grep total | awk '{print "Total coverage: " $$3}'

test-watch: ## Run tests in watch mode (requires entr)
	@command -v entr > /dev/null 2>&1 || { echo "$(RED)entr not installed. Install with: brew install entr$(NC)"; exit 1; }
	find . -name '*.go' | entr -c make test-unit

##@ Code Quality

lint: ## Run linter
	@echo "$(GREEN)Running linter...$(NC)"
	@command -v $(GOLINT) > /dev/null 2>&1 || { echo "$(RED)golangci-lint not installed. Run 'make setup' first$(NC)"; exit 1; }
	$(GOLINT) run ./...

lint-fix: ## Run linter and fix issues
	@echo "$(GREEN)Running linter with auto-fix...$(NC)"
	$(GOLINT) run --fix ./...

fmt: ## Format code
	@echo "$(GREEN)Formatting code...$(NC)"
	@command -v $(GOFMT) > /dev/null 2>&1 || { echo "Using go fmt instead of gofumpt"; go fmt ./...; exit 0; }
	$(GOFMT) -l -w .

vet: ## Run go vet
	@echo "$(GREEN)Running go vet...$(NC)"
	go vet ./...

check: fmt vet lint test-unit ## Run all checks (fmt, vet, lint, test)
	@echo "$(GREEN)All checks passed!$(NC)"

##@ Blockchain

testnet-balance: ## Check testnet wallet balance
	@echo "$(GREEN)Checking testnet balance...$(NC)"
	@./scripts/check-balance.sh

testnet-transfer: ## Send test USDC (requires AMOUNT and TO env vars)
	@echo "$(GREEN)Sending testnet USDC...$(NC)"
	go run ./cmd/transfer/main.go

##@ Docker

docker-build: ## Build Docker image
	docker build -t alancoin:$(VERSION) .

docker-run: ## Run Docker container
	docker run -p 8080:8080 --env-file .env alancoin:$(VERSION)

docker-compose-up: ## Start all services with docker-compose
	docker-compose up -d

docker-compose-down: ## Stop all services
	docker-compose down

##@ Deployment

deploy: ## Deploy to Fly.io
	@./scripts/deploy.sh

fly-logs: ## View Fly.io logs
	fly logs

fly-status: ## Check Fly.io app status
	fly status

fly-ssh: ## SSH into Fly.io instance
	fly ssh console

##@ Database

seed: ## Seed database with demo data
	@echo "$(GREEN)Seeding database...$(NC)"
	@./scripts/seed.sh

db-setup: ## Set up local PostgreSQL database
	@echo "$(GREEN)Setting up database...$(NC)"
	@./scripts/setup-db.sh

# db-migrate: ## Run database migrations
# 	@echo "$(GREEN)Running migrations...$(NC)"
# 	go run ./cmd/migrate/main.go up

# db-rollback: ## Rollback last migration
# 	go run ./cmd/migrate/main.go down

##@ SDK

sdk-test: ## Run Python SDK tests
	@echo "$(GREEN)Running SDK tests...$(NC)"
	cd sdks/python && pip install -e ".[dev]" -q && pytest tests/ -v

sdk-install: ## Install Python SDK locally
	cd sdks/python && pip install -e .

##@ Release

version: ## Show version info
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
