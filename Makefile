.PHONY: help check ci-workflows-check ci-evidence-check test vet lint fmt lengths-check hooks-install wiki-preview vuln tools-outdated semgrep semgrep-ci live live-write browser-creds gen-docs

SHELL := bash

help: ## Show this help
	@bash -c "grep '^[a-zA-Z_-]*:.*## ' $(MAKEFILE_LIST) | sort | sed 's/:.*## /\t/'"

check: test vet lint lengths-check ci-workflows-check ci-evidence-check ## Run every local check; run before each commit

ci-workflows-check: ## Check workflow triggers, source identity, and validation gates
	@python3 scripts/test-ci-workflows.py

ci-evidence-check: ## Check offline landing and release evidence rules
	@python3 scripts/test-ci-evidence.py

test: ## Run tests
	@go test -race ./...

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

vuln: ## Scan dependencies and the standard library for known vulnerabilities
	@govulncheck ./...

tools-outdated: ## Fail when Go, a pinned tool, or a GitHub Action has a newer release
	@bash scripts/tools-outdated.sh

semgrep: ## Offline Semgrep scan with registry packs; CI runs the connected scan
	@semgrep --config p/golang --config p/gosec --config p/secrets --error .

semgrep-ci: ## Connected Semgrep Pro scan (Code, Supply Chain), as CI runs it; needs SEMGREP_APP_TOKEN
	@semgrep ci --code --supply-chain --no-suppress-errors

live: ## Run live smoke tests against the real API (reads .env)
	@go test -tags live -count=1 -v -run TestLive ./livetest/

live-write: ## Run the gated live billing write test; needs owner approval per run and VNGCLOUD_LIVE_WRITE=1
	@go test -tags livewrite -count=1 -timeout 60m -v -run TestLiveWrite ./livetest/

gen-docs: ## Regenerate the CLI reference pages in docs/wiki/ from the operation tables
	@go run ./cmd/vngcloud gen-docs docs/wiki

browser-creds: ## Serve .env IAM User values once to scripts/browser-login.js for Playwright MCP
	@python3 scripts/browser-creds.py

.DEFAULT_GOAL := help
