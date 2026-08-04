SHELL := /bin/bash

# Load .env if it exists so `make api` works without exporting anything by hand.
-include .env
export

.DEFAULT_GOAL := help
.PHONY: help up down logs ps api test test-go test-int e2e smoke lint fmt tidy check \
        migrate-up migrate-down migrate-reset migrate-status migrate-new sqlc \
        web web-install web-build web-lint docker-build docker-run

help: ## Show the available targets
	@# -h suppresses the filename. MAKEFILE_LIST holds two entries whenever a
	@# .env exists — the -include above adds it — and with more than one file
	@# grep prefixes every line, so awk split on "Makefile:" and printed that as
	@# the target name for everything. It worked on a fresh clone and nowhere
	@# else.
	@#
	@# [0-9] in the class as well, or a target like e2e never appears.
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
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

# One config per module, each owning its own tables. There is no root sqlc.yaml
# on purpose: adding a module means adding a config next to that module's
# queries, not editing a shared file every module has to agree about.
SQLC_CONFIGS := $(shell find internal/modules -name sqlc.yaml | sort)

sqlc: ## Regenerate the type-safe query code for every module
	@test -n "$(SQLC_CONFIGS)" || { echo "no module sqlc.yaml found"; exit 1; }
	@for cfg in $(SQLC_CONFIGS); do \
		echo "sqlc: $$cfg"; \
		go tool sqlc -f $$cfg generate || exit 1; \
	done

contract: ## Regenerate web/lib/contract.ts from the API's own bounds and vocabularies
	go run ./cmd/gencontract

# ── Go ───────────────────────────────────────────────────────────────────────

api: ## Run the API (requires `make up`)
	go run ./cmd/api

test-go: ## Run the Go tests with the race detector
	go test ./... -race -cover

arch: ## Check the module boundaries and layer direction
	@# Named separately from `make test` — which already runs it — so the
	@# failure has somewhere to be reproduced from, and so `make arch` is the
	@# obvious thing to run after moving a package.
	@#
	@# This is the transitive half. The fast half is depguard, in `make lint`:
	@# it catches a direct illegal import in seconds, and misses a module
	@# reached through two hops. Both gates run in CI.
	go test ./internal/architecture/ -count=1 -v

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
	go tool golangci-lint run --build-tags=smoke ./...
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

e2e: ## Run the end-to-end suite (requires `make up`, `make api`, and a dev Clerk instance)
	cd web && pnpm e2e

smoke: ## Check the deployed application. Reads only — safe against production
	go test -tags=smoke ./smoke/... -count=1

# ── Gates ────────────────────────────────────────────────────────────────────

test: test-go ## Run every test suite
	@# The build is a test too: `next build` type-checks, and a type error is a
	@# broken deploy. Guarded on node_modules so a Go-only clone still passes.
	@if [ -d web/node_modules ]; then cd web && pnpm test && pnpm build; fi

check: lint test ## Everything that must pass before a commit
	@echo "check: ok"
