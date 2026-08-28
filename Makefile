# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT

GO := go
GOFMT := gofmt
BUILD_DIR := bin
# Separate modules: ./... from the root does not reach these — an
# untested submodule is an unpublishable one waiting to happen.
SUBMODULES := x/config x/comparators examples/checkout examples/misuse

.DEFAULT_GOAL := help

.PHONY: build
build: ## Build all packages
	$(GO) build ./...

.PHONY: test
test: ## Run unit tests (root + submodules)
	$(GO) test -count=1 -shuffle=on -timeout=60s ./...
	@for m in $(SUBMODULES); do (cd $$m && $(GO) test -count=1 -shuffle=on -timeout=60s ./...) || exit 1; done

.PHONY: test-race
test-race: ## Run tests with the race detector (root + submodules)
	$(GO) test -race -count=1 -shuffle=on -timeout=120s ./...
	@for m in $(SUBMODULES); do (cd $$m && $(GO) test -race -count=1 -shuffle=on -timeout=120s ./...) || exit 1; done

.PHONY: test-cover
test-cover: ## Generate an atomic coverage profile
	$(GO) test -count=1 -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

.PHONY: fmt
fmt: ## Format Go sources
	$(GOFMT) -s -w .

.PHONY: fmt-check
fmt-check: ## Fail when Go sources are not formatted
	@test -z "$$($(GOFMT) -s -l .)" || { \
		echo "Files not formatted:"; \
		$(GOFMT) -s -l .; \
		exit 1; \
	}

.PHONY: vet
vet: ## Run go vet (root + submodules)
	$(GO) vet ./...
	@for m in $(SUBMODULES); do (cd $$m && $(GO) vet ./...) || exit 1; done

.PHONY: lint
lint: fmt-check vet ## Run golangci-lint in addition to core checks
	golangci-lint run ./...

.PHONY: mod-tidy-check
mod-tidy-check: ## Verify go.mod and go.sum are tidy
	@phase_tidy_dir=$$(mktemp -d); \
	trap 'rm -rf "$$phase_tidy_dir"' EXIT; \
	cp go.mod "$$phase_tidy_dir/go.mod"; \
	test ! -f go.sum || cp go.sum "$$phase_tidy_dir/go.sum"; \
	$(GO) mod tidy; \
	cmp -s go.mod "$$phase_tidy_dir/go.mod" || { echo "go.mod is not tidy; run go mod tidy"; exit 1; }; \
	if test -f "$$phase_tidy_dir/go.sum"; then \
		cmp -s go.sum "$$phase_tidy_dir/go.sum" || { echo "go.sum is not tidy; run go mod tidy"; exit 1; }; \
	elif test -f go.sum; then \
		echo "go.sum was generated; run go mod tidy"; exit 1; \
	fi
	@for m in $(SUBMODULES); do (cd $$m && $(GO) mod tidy -diff) || { echo "$$m: go.mod/go.sum not tidy"; exit 1; }; done

.PHONY: vulncheck
vulncheck: ## Run Go vulnerability analysis (root + submodules; requires govulncheck)
	govulncheck ./...
	@for m in $(SUBMODULES); do (cd $$m && govulncheck ./...) || exit 1; done

.PHONY: secrets-check
secrets-check: ## Scan the working tree for secrets (requires gitleaks)
	gitleaks dir . --redact --no-banner --exit-code 1

.PHONY: deps
deps: ## Download and verify module dependencies
	$(GO) mod download
	$(GO) mod verify

.PHONY: check
check: fmt-check vet test ## Run the fast local quality gate

.PHONY: ci
ci: mod-tidy-check fmt-check vet test-race test-cover ## Run the core CI gate

.PHONY: clean
clean: ## Remove local build and coverage artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
