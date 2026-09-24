.PHONY: help check test vet lint fmt lengths-check hooks-install wiki-preview semgrep live

SHELL := bash

help: ## Show this help
	@bash -c "grep '^[a-zA-Z_-]*:.*## ' $(MAKEFILE_LIST) | sort | sed 's/:.*## /\t/'"

check: test vet lint lengths-check ## Run every local check; run before each commit

test: ## Run tests
	@go test ./...

vet: ## Run go vet
	@go vet ./...

lint: ## Run golangci-lint
	@golangci-lint run ./...

fmt: ## Auto-fix formatting and import sorting
	@golangci-lint fmt ./...

lengths-check: ## Fail when a doc passes 450 lines or Go code passes 700
	@bash scripts/check-lengths.sh

hooks-install: ## Point git at .githooks so pre-commit runs gitleaks and the length check
	@git config core.hooksPath .githooks
	@echo "hooks installed from .githooks"

wiki-preview: ## Render docs/wiki/ as the wiki will serve it, without pushing
	@bash scripts/wiki-sync.sh --dry-run

semgrep: ## Offline Semgrep scan with registry packs; CI runs the connected scan
	@semgrep --config p/golang --config p/gosec --config p/secrets --error .

live: ## Run live smoke tests against the real API (reads .env)
	@go test -tags live -count=1 -v -run TestLive .

.DEFAULT_GOAL := help
