.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Variables
FRONTEND_REF ?= local/dalec/frontend
DALEC_DISABLE_DIFF_MERGE ?= 0
TIMEOUT ?= 10m
INTEGRATION_TIMEOUT ?= 59m
GO ?= go
DOCKER ?= docker

# Build output directory
BUILD_DIR ?= _output
BIN_DIR ?= $(BUILD_DIR)/bin

# Go build flags
GO_BUILD_FLAGS ?= -v
GO_TEST_FLAGS ?= -v

# Integration test suite (can be overridden: make test-integration SUITE=Mariner2)
SUITE ?=

##@ Development

.PHONY: generate
generate: ## Generate required source files
	$(GO) generate ./...

.PHONY: lint
lint: ## Run custom linters
	$(GO) run ./cmd/lint ./...

.PHONY: lint-all
lint-all: ## Run golangci-lint and custom linters
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=30m; \
	else \
		echo "golangci-lint not found. Install it or use 'make docker-lint'"; \
		echo "See: https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi
	$(GO) run ./cmd/lint ./...

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w -s .

##@ Build

.PHONY: build
build: build-frontend build-redirectio ## Build all binaries

.PHONY: build-frontend
build-frontend: ## Build frontend binary
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(BIN_DIR)/frontend ./cmd/frontend

.PHONY: build-redirectio
build-redirectio: ## Build dalec-redirectio binary
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(BIN_DIR)/dalec-redirectio ./cmd/dalec-redirectio

.PHONY: build-all-tools
build-all-tools: ## Build all CLI tools
	@mkdir -p $(BIN_DIR)
	@for dir in cmd/*; do \
		if [ -d "$$dir" ]; then \
			tool=$$(basename $$dir); \
			echo "Building $$tool..."; \
			$(GO) build $(GO_BUILD_FLAGS) -o $(BIN_DIR)/$$tool ./$$dir || exit 1; \
		fi \
	done

.PHONY: install
install: ## Install binaries to $GOBIN or $GOPATH/bin
	$(GO) install ./cmd/frontend
	$(GO) install ./cmd/dalec-redirectio

##@ Testing

.PHONY: test
test: ## Run unit tests
	$(GO) test --test.short --timeout=$(TIMEOUT) ./...

.PHONY: test-verbose
test-verbose: ## Run unit tests with verbose output
	$(GO) test -v --test.short --timeout=$(TIMEOUT) ./...

.PHONY: test-short
test-short: ## Run only short unit tests
	$(GO) test --test.short ./...

.PHONY: test-race
test-race: ## Run tests with race detector
	$(GO) test -race --test.short --timeout=$(TIMEOUT) ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	$(GO) test --test.short -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-integration
test-integration: ## Run integration tests for a specific suite (use SUITE=<name>)
	@if [ -z "$(SUITE)" ]; then \
		echo "Error: SUITE variable must be set. Example: make test-integration SUITE=Mariner2"; \
		echo "Available suites: Mariner2, Azlinux3, Bookworm, Bullseye, Bionic, Focal, Jammy, Noble, Windows, Almalinux8, Almalinux9, Rockylinux8, Rockylinux9"; \
		exit 1; \
	fi
	$(GO) test -timeout=$(INTEGRATION_TIMEOUT) -v -run=$(SUITE) ./test

.PHONY: test-integration-all
test-integration-all: ## Run all integration tests (45+ minutes)
	$(GO) test -timeout=$(INTEGRATION_TIMEOUT) -v ./test

##@ Docker

.PHONY: docker-frontend
docker-frontend: ## Build frontend Docker image
	FRONTEND_REF=$(FRONTEND_REF) $(DOCKER) buildx bake frontend

.PHONY: docker-lint
docker-lint: ## Run linting in Docker
	$(DOCKER) buildx bake lint

##@ Documentation

.PHONY: docs-deps
docs-deps: ## Install documentation dependencies
	cd website && npm install

.PHONY: docs-serve
docs-serve: ## Serve documentation locally at http://localhost:3000
	cd website && npm start

.PHONY: docs-build
docs-build: ## Build documentation for production
	cd website && npm run build

.PHONY: schema
schema: ## Generate JSON schema
	$(GO) run ./cmd/gen-jsonschema > docs/spec.schema.json

##@ Validation

.PHONY: verify
verify: generate lint test check-generated ## Run all verification steps (unit tests, linting, generation check)

.PHONY: check-generated
check-generated: ## Verify that generated files are up to date
	@echo "Checking if generated files are up to date..."
	@if ! git diff --exit-code; then \
		echo "Error: Generated files are out of date. Please run 'make generate' and commit the changes."; \
		exit 1; \
	fi
	@echo "Generated files are up to date."

.PHONY: validate-ci
validate-ci: deps generate lint test check-generated ## Run all CI validation steps

##@ Cleanup

.PHONY: clean
clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

.PHONY: clean-all
clean-all: clean ## Clean all generated files and caches
	$(GO) clean -cache -testcache -modcache
	rm -rf website/node_modules
	rm -rf website/build

##@ Utilities

.PHONY: version
version: ## Show Go and Docker versions
	@echo "Go version:"
	@$(GO) version
	@echo ""
	@echo "Docker version:"
	@$(DOCKER) version --format '{{.Client.Version}}'
	@echo ""
	@echo "Docker Buildx version:"
	@$(DOCKER) buildx version

.PHONY: mod-tidy
mod-tidy: ## Tidy Go modules
	$(GO) mod tidy

.PHONY: mod-verify
mod-verify: ## Verify Go modules
	$(GO) mod verify

.PHONY: list-tools
list-tools: ## List all available CLI tools
	@echo "Available CLI tools in cmd/:"
	@ls -1 cmd/

##@ Default

.DEFAULT_GOAL := help
