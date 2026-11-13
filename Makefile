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

.PHONY: seed-dev
seed-dev: ## DEV/TEST ONLY: seed fixture-backed instruments against DB_URL -- never run against production
	docker run --rm --network host -v $(PWD)/db/seed:/seed postgres:16-alpine psql "$(DB_URL)" -f /seed/dev_fixture_instruments.sql

.PHONY: generate
generate: ## Run sqlc and oapi-codegen
	sqlc generate
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

.PHONY: run-api
run-api: ## Run the API server locally
	go run ./cmd/argus-api

.PHONY: run-worker
run-worker: ## Run the worker locally
	go run ./cmd/argus-worker

LIVE_DEMO_SPEED ?= 60

.PHONY: live-demo
live-demo: ## THE ONE COMMAND to watch ticks move on screen (live-chart brief, Part 2G): seeds the dev fixture against DB_URL (default: the dev argus database), then runs the worker with the one-time historical ingest AND a continuing live replay -- new candles forming and closing forward from each instrument's own last historical close, published through the hub exactly like a real feed. Needs: make infra, then make run-api (or make up), already running; open the frontend (make web-dev) on /analysis for BTC-PERP or ETH-PERP to watch it. Blocks in the foreground (Ctrl-C stops it); LIVE_DEMO_SPEED overrides the pace (default 60: about one new candle per real second).
	$(MAKE) seed-dev DB_URL="$(DB_URL)"
	ARGUS_DEV_FIXTURE_INGEST=true ARGUS_DEV_FIXTURE_LIVE_REPLAY=true ARGUS_DEV_FIXTURE_LIVE_REPLAY_SPEED="$(LIVE_DEMO_SPEED)" go run ./cmd/argus-worker

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
	$(MAKE) seed-dev DB_URL="$(E2E_DB_URL)"
	ARGUS_DATABASE_URL="$(E2E_DB_URL)" ARGUS_REDIS_URL="$(E2E_REDIS_URL)" ARGUS_HTTP_PORT="$(E2E_HTTP_PORT)" ARGUS_DEV_FIXTURE_INGEST=true ARGUS_DEV_FIXTURE_LIVE_REPLAY=true ARGUS_DEV_FIXTURE_LIVE_REPLAY_SPEED="$(LIVE_DEMO_SPEED)" go run ./cmd/argus-worker &
	sleep 2
	ARGUS_DATABASE_URL="$(E2E_DB_URL)" ARGUS_REDIS_URL="$(E2E_REDIS_URL)" ARGUS_HTTP_PORT="$(E2E_HTTP_PORT)" ARGUS_AUTH_ALLOWED_ORIGINS="http://localhost:4173,http://127.0.0.1:4173" go run ./cmd/argus-api 2>&1 | tee "$(E2E_API_LOG)"
