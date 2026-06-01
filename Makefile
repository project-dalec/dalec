.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Variables
FRONTEND_REF ?= local/dalec/frontend
TIMEOUT ?= 10m
INTEGRATION_TIMEOUT ?= 59m
GO ?= go

# Integration test suite (can be overridden: make test-integration SUITE=Azlinux3)
SUITE ?=

##@ Development

.PHONY: generate
generate: ## Generate required source files
	$(GO) generate ./...

.PHONY: lint
lint: ## Run linters
	docker buildx bake lint
	$(GO) run ./cmd/lint ./...

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w -s .

##@ Build

.PHONY: build
build: frontend ## Build frontend image

.PHONY: frontend
frontend: ## Build frontend Docker image using docker buildx bake
	FRONTEND_REF=$(FRONTEND_REF) docker buildx bake frontend

.PHONY: examples
examples: ## Build example specs
	docker buildx bake examples

##@ Testing

.PHONY: test
test: ## Run unit tests
	$(GO) test --test.short -race --timeout=$(TIMEOUT) ./...

.PHONY: test-integration
test-integration: ## Run integration tests. Use SUITE=<name> to run specific test suite (e.g., make test-integration SUITE=Mariner2)
	@if [ -n "$(SUITE)" ]; then \
		echo "Running integration test suite: $(SUITE)"; \
		$(GO) test --timeout=$(INTEGRATION_TIMEOUT) -v ./test -run=Test$(SUITE); \
	else \
		echo "Running all integration tests"; \
		$(GO) test --timeout=$(INTEGRATION_TIMEOUT) -v ./test; \
	fi

.PHONY: test-bake
test-bake: ## Run tests via docker buildx bake
	docker buildx bake test

##@ Documentation

.PHONY: docs-serve
docs-serve: ## Build and serve documentation locally at http://localhost:3000
	$(GO) -C ./website run .

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

##@ Cleanup

.PHONY: clean
clean: ## Clean build artifacts
	rm -rf _output/
	rm -f coverage.out coverage.html

##@ Default

.DEFAULT_GOAL := help
