# ARGUS developer commands.
SHELL := /bin/bash
MIGRATE_VERSION := v4.17.1
DB_URL ?= postgres://argus:argus@localhost:5432/argus?sslmode=disable
MIGRATIONS_DIR := db/migrations

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
