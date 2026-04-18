.PHONY: help test vet lint fmt

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

.DEFAULT_GOAL := help
