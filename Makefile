SHELL := /bin/bash

# Load .env if it exists so `make api` works without exporting anything by hand.
-include .env
export

.DEFAULT_GOAL := help
.PHONY: help up down logs ps api test test-go lint fmt tidy check \
        web web-install web-build web-lint

help: ## Show the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ── Local infrastructure ─────────────────────────────────────────────────────

up: ## Start Postgres and Redis, waiting until they are healthy
	docker compose up -d --wait

down: ## Stop the containers, keeping the volumes
	docker compose down

logs: ## Follow container logs
	docker compose logs -f

ps: ## Show container status
	docker compose ps

# ── Go ───────────────────────────────────────────────────────────────────────

api: ## Run the API (requires `make up`)
	go run ./cmd/api

test-go: ## Run the Go tests with the race detector
	go test ./... -race -cover

fmt: ## Format the Go sources
	gofmt -w .

tidy: ## Prune and verify module requirements
	go mod tidy

lint: ## Vet the Go sources and check formatting
	go vet ./...
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt would change:"; echo "$$unformatted"; exit 1; \
	fi

# ── Web ──────────────────────────────────────────────────────────────────────

web-install: ## Install web dependencies
	cd web && pnpm install

web: ## Run the Next.js dev server
	cd web && pnpm dev

web-build: ## Build the Next.js app
	cd web && pnpm build

web-lint: ## Lint the web sources
	cd web && pnpm lint

# ── Gates ────────────────────────────────────────────────────────────────────

test: test-go ## Run every test suite
	@if [ -d web/node_modules ]; then cd web && pnpm build; fi

check: lint test ## Everything that must pass before a commit
	@echo "check: ok"
