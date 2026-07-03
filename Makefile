.PHONY: help test vet lint fmt live

SHELL := bash

help: ## Show this help
	@bash -c "grep '^[a-zA-Z_-]*:.*## ' $(MAKEFILE_LIST) | sort | sed 's/:.*## /\t/'"

test: ## Run tests
	@go test ./...

vet: ## Run go vet
	@go vet ./...

lint: ## Run golangci-lint
	@golangci-lint run ./...

fmt: ## Auto-fix formatting and import sorting
	@golangci-lint fmt ./...

live: ## Run live smoke tests against the real API (reads .env)
	@go test -tags live -count=1 -v -run TestLive .

.DEFAULT_GOAL := help
