# ARGUS developer commands.
SHELL := /bin/bash
MIGRATE_VERSION := v4.17.1
DB_URL ?= postgres://argus:argus@localhost:5432/argus?sslmode=disable
MIGRATIONS_DIR := db/migrations

# e2e-stack's own target database/cache/port -- its own dedicated
# database (argus_e2e), never the dev argus database and never
# argus_test (web/e2e's own Playwright suite, live-chart brief phase
# 4). A dedicated database, dropped and recreated by e2e-resetdb below
# before every e2e-stack run, is what makes "fresh database per e2e
# run" possible: argus_test is also the Go integration suite's own
# shared database (internal/*/integration_test.go's own
# ARGUS_TEST_DATABASE_URL default), so dropping IT on every e2e run
# would be destructive to unrelated work; a separate database the e2e
# suite owns outright has no such conflict. Redis logical DB 14 is
# distinct from the dev worker's own 0 and the Go integration suite's
# own 15, so none of the three can cross-publish on the same pub/sub
# channels.
E2E_DB_NAME := argus_e2e
E2E_DB_URL ?= postgres://argus:argus@localhost:5432/$(E2E_DB_NAME)?sslmode=disable
E2E_MAINTENANCE_URL ?= postgres://argus:argus@localhost:5432/postgres?sslmode=disable
E2E_REDIS_URL ?= redis://localhost:6379/14
E2E_HTTP_PORT ?= 8090
E2E_API_LOG := /tmp/argus-e2e-api.log

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: doctor
doctor: ## Check local toolchain versions
	@bash scripts/doctor.sh

.PHONY: tools
tools: ## Install Go dev tools (sqlc, oapi-codegen, migrate, govulncheck)
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: setup
setup: ## Download deps and copy .env
	go mod download
	test -f .env || cp .env.example .env
	cd web && (pnpm install || npm install)

.PHONY: up
up: ## Start postgres, redis, api and worker
	docker compose up -d --build

.PHONY: infra
infra: ## Start only postgres and redis
	docker compose up -d postgres redis

.PHONY: down
down: ## Stop all services
	docker compose down

.PHONY: migrate-up
migrate-up: ## Apply all up migrations
	docker run --rm --network host -v $(PWD)/$(MIGRATIONS_DIR):/m migrate/migrate:$(MIGRATE_VERSION) -path=/m -database "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back one migration
	docker run --rm --network host -v $(PWD)/$(MIGRATIONS_DIR):/m migrate/migrate:$(MIGRATE_VERSION) -path=/m -database "$(DB_URL)" down 1

.PHONY: migrate-create
migrate-create: ## Create a migration: make migrate-create name=add_x
	docker run --rm -v $(PWD)/$(MIGRATIONS_DIR):/m migrate/migrate:$(MIGRATE_VERSION) create -ext sql -dir /m -seq $(name)

.PHONY: seed-e2e-fixtures
seed-e2e-fixtures: ## E2E/TEST ONLY: seed the fixture instruments (db/seed/dev_fixture_instruments.sql) against DB_URL -- called by e2e-stack; never run against the dev or a production database (Phase 2, D2: the product shows real data only)
	docker run --rm --network host -v $(PWD)/db/seed:/seed postgres:16-alpine psql "$(DB_URL)" -f /seed/dev_fixture_instruments.sql

.PHONY: seed-e2e-only
seed-e2e-only: ## E2E ONLY: seed the e2e-only fixture instrument(s) (db/seed/e2e_only_instruments.sql) against DB_URL -- called by e2e-stack alone, so this never reaches a developer's own local dev database
	docker run --rm --network host -v $(PWD)/db/seed:/seed postgres:16-alpine psql "$(DB_URL)" -f /seed/e2e_only_instruments.sql

.PHONY: generate
generate: ## Run sqlc and oapi-codegen
	sqlc generate
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

.PHONY: run-api
run-api: ## Run the API server locally
	go run ./cmd/argus-api

.PHONY: run-worker
run-worker: ## Run the worker locally (needs ARGUS_INGEST_PROVIDER set, e.g. hyperliquid in .env -- it refuses to start without it)
	go run ./cmd/argus-worker

.PHONY: sync-instruments
sync-instruments: ## Register the Hyperliquid instruments (ARGUS_HYPERLIQUID_SYMBOLS) as DISABLED rows and print the enable SQL to run by hand -- nothing in code ever enables an instrument (ADR-007)
	go run ./cmd/argus-instruments sync-hyperliquid

# E2E_REPLAY_SPEED paces the e2e stack's fixture live replay (about one
# new candle per real second at 60).
E2E_REPLAY_SPEED ?= 60

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w .

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run tests with race detector and coverage
	go test -race -cover ./...

.PHONY: test-integration
test-integration: ## Run integration tests against a real Postgres (needs: make infra && make migrate-up)
	go test -tags=integration -race -cover ./...

.PHONY: vuln
vuln: ## Run govulncheck
	govulncheck ./...

.PHONY: build
build: ## Build binaries into ./bin
	mkdir -p bin
	go build -o bin/argus-api ./cmd/argus-api
	go build -o bin/argus-worker ./cmd/argus-worker

.PHONY: check
check: fmt vet lint test ## Run the full local check suite

.PHONY: web-dev
web-dev: ## Run the frontend dev server
	cd web && (pnpm dev || npm run dev)

.PHONY: web-build
web-build: ## Build the frontend
	cd web && (pnpm build || npm run build)

.PHONY: e2e-resetdb
e2e-resetdb: ## Drops and recreates the dedicated e2e database (argus_e2e) -- "fresh database per e2e run": a previous run's own live-replay candles (which can run for a while, advancing far into its own simulated future) must never leak into the next run's "most recent N" queries. Never argus_test, never argus: this is its own database, owned outright by e2e-stack. Also flushes Redis logical DB 14 (its own INCR sequence counters are keyed by instrument id, which is always fresh after the database reset above, but a stale key left behind from a killed run is not worth tracking down by hand).
	docker run --rm --network host postgres:16-alpine psql "$(E2E_MAINTENANCE_URL)" -c "DROP DATABASE IF EXISTS $(E2E_DB_NAME);"
	docker run --rm --network host postgres:16-alpine psql "$(E2E_MAINTENANCE_URL)" -c "CREATE DATABASE $(E2E_DB_NAME);"
	docker run --rm --network host redis:7-alpine redis-cli -h localhost -n 14 FLUSHDB

.PHONY: e2e-stack
e2e-stack: e2e-resetdb ## THE ONE COMMAND for web/e2e's own backend: resets argus_e2e to a clean database, migrates + seeds + ingests the dev fixture into it, then runs a dedicated api+worker pair on E2E_HTTP_PORT (default 8090), with CORS/CSRF allowing Vite preview's own origin (4173). The worker also runs Part 2G's own live replay, so a forming candle visibly moves and a new one appears at the boundary -- exactly what the Playwright scenario in web/e2e needs. Needs: make infra running first (postgres+redis). Blocks in the foreground (Ctrl-C stops both); api's own stdout/stderr -- including the dev mailer's logged signup codes -- goes to $(E2E_API_LOG), which web/e2e's own global setup reads.
	$(MAKE) migrate-up DB_URL="$(E2E_DB_URL)"
	$(MAKE) seed-e2e-fixtures DB_URL="$(E2E_DB_URL)"
	$(MAKE) seed-e2e-only DB_URL="$(E2E_DB_URL)"
	ARGUS_INGEST_PROVIDER=fixture ARGUS_DATABASE_URL="$(E2E_DB_URL)" ARGUS_REDIS_URL="$(E2E_REDIS_URL)" ARGUS_HTTP_PORT="$(E2E_HTTP_PORT)" ARGUS_DEV_FIXTURE_INGEST=true ARGUS_DEV_FIXTURE_LIVE_REPLAY=true ARGUS_DEV_FIXTURE_LIVE_REPLAY_SPEED="$(E2E_REPLAY_SPEED)" go run ./cmd/argus-worker &
	sleep 2
	ARGUS_DATABASE_URL="$(E2E_DB_URL)" ARGUS_REDIS_URL="$(E2E_REDIS_URL)" ARGUS_HTTP_PORT="$(E2E_HTTP_PORT)" ARGUS_AUTH_ALLOWED_ORIGINS="http://localhost:4173,http://127.0.0.1:4173" go run ./cmd/argus-api 2>&1 | tee "$(E2E_API_LOG)"
