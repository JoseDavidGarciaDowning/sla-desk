SHELL := /bin/bash

# Load .env if it exists so `make api` works without exporting anything by hand.
-include .env
export

.DEFAULT_GOAL := help
.PHONY: help up down logs ps api test test-go test-int lint fmt tidy check \
        migrate-up migrate-down migrate-reset migrate-status migrate-new sqlc \
        web web-install web-build web-lint docker-build docker-run

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

# ── Database ─────────────────────────────────────────────────────────────────
#
# goose and sqlc are pinned as tool dependencies in go.mod and run through
# `go tool`. A fresh clone needs nothing installed beyond Go itself, and CI
# generates with the same version you do.

GOOSE := go tool goose -dir db/migrations postgres "$(DATABASE_URL)"

migrate-up: ## Apply every pending migration
	$(GOOSE) up

migrate-down: ## Roll back the most recent migration
	$(GOOSE) down

migrate-reset: ## Roll every migration back, then apply them all again
	$(GOOSE) reset
	$(GOOSE) up

migrate-status: ## Show which migrations are applied
	$(GOOSE) status

migrate-new: ## Create a migration: make migrate-new name=add_tickets
	@test -n "$(name)" || { echo "usage: make migrate-new name=add_tickets"; exit 1; }
	go tool goose -dir db/migrations create $(name) sql

sqlc: ## Regenerate the type-safe query code from db/queries
	go tool sqlc generate

contract: ## Regenerate web/lib/contract.ts from the API's own bounds and vocabularies
	go run ./cmd/gencontract

# ── Go ───────────────────────────────────────────────────────────────────────

api: ## Run the API (requires `make up`)
	go run ./cmd/api

test-go: ## Run the Go tests with the race detector
	go test ./... -race -cover

test-int: ## Run the integration tests against the local Postgres (requires `make up`)
	@# Without DATABASE_URL every integration test calls t.Skip and `go test`
	@# still prints ok. Failing here instead means a green run is a real one.
	@test -n "$(DATABASE_URL)" || { \
		echo "DATABASE_URL is not set — the integration tests would all skip and still report ok."; \
		echo "Copy .env.example to .env, or export it, then try again."; \
		exit 1; \
	}
	$(MAKE) migrate-up
	go test ./... -race -tags=integration -count=1

fmt: ## Format the Go sources
	go tool golangci-lint fmt ./...

tidy: ## Prune and verify module requirements
	go mod tidy

lint: ## Lint every source, Go and web
	go tool golangci-lint run ./...
	@# Build-tagged files are invisible to the run above, so the integration
	@# tests would rot unnoticed until someone next ran them.
	go tool golangci-lint run --build-tags=integration ./...
	@# The web sources too. This was missing until T15, and the gap was not
	@# theoretical: ESLint had been reporting a React purity error in
	@# sla-timer.tsx that nothing ran, so nothing saw. A linter that only one
	@# target invokes, and no gate does, is a linter nobody runs.
	@if [ -d web/node_modules ]; then cd web && pnpm lint; fi

# ── Container ────────────────────────────────────────────────────────────────

docker-build: ## Build the API image
	docker build -t sla-desk-api:local .

docker-run: docker-build ## Run the API image against the local Postgres
	docker run --rm -p 8082:8080 \
		-e DATABASE_URL="postgres://sladesk:sladesk@host.docker.internal:5433/sladesk?sslmode=disable" \
		sla-desk-api:local

# ── Web ──────────────────────────────────────────────────────────────────────

web-install: ## Install web dependencies
	cd web && pnpm install

web: ## Run the Next.js dev server
	cd web && pnpm dev

web-build: ## Build the Next.js app
	cd web && pnpm build

web-lint: ## Lint the web sources
	cd web && pnpm lint

web-test: ## Run the Vitest suites (requires `make web-install`)
	cd web && pnpm test

# ── Gates ────────────────────────────────────────────────────────────────────

test: test-go ## Run every test suite
	@# The build is a test too: `next build` type-checks, and a type error is a
	@# broken deploy. Guarded on node_modules so a Go-only clone still passes.
	@if [ -d web/node_modules ]; then cd web && pnpm test && pnpm build; fi

check: lint test ## Everything that must pass before a commit
	@echo "check: ok"
