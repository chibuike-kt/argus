# ARGUS

Autonomous Market Intelligence. Always watching. Always adapting.

ARGUS monitors crypto perpetual futures and forex, runs deterministic,
versioned, multi-timeframe technical analysis, and proposes trade plans with
the evidence behind them. It does not place real orders (ADR-001).

This repository is the project skeleton. Most strategy and product rules are
still open decisions; see `docs/adr`.

## Stack

- Go 1.26 modular monolith (API + worker)
- PostgreSQL (sqlc) and Redis
- golang-migrate (expand-contract migrations)
- React + TypeScript + Vite frontend
- OpenAPI-first REST under `/api/v1`, WebSockets for live updates
- GitHub Actions CI, CodeQL, dependency review

Full rationale is in `docs/adr` (ADR-001 to ADR-020). Start at `docs/adr/README.md`.

## Prerequisites

- Go 1.26+
- Node 22+ and pnpm 9+
- Docker and Docker Compose
- Git

Check your machine:

```bash
make doctor
```

Install the Go dev tools (sqlc, oapi-codegen, migrate, govulncheck):

```bash
make tools
```

## Quick start

```bash
# 1. deps and env
make setup

# 2. start postgres + redis
make infra

# 3. apply migrations
make migrate-up

# 4. seed dev/test instruments (DEV/TEST ONLY, never against production)
make seed-dev

# 5. run the API (new terminal)
make run-api

# 6. run the worker with the dev dataset replay (new terminal) -- without
# ARGUS_DEV_FIXTURE_INGEST the worker has nothing to process: nothing else
# feeds it a candle yet (ADR-007 is still open, see docs/status/project-status.md)
ARGUS_DEV_FIXTURE_INGEST=true make run-worker

# 7. frontend dev server (new terminal) -- wired to the real API above
# (web/src/api); set VITE_USE_FIXTURES=true in web/.env instead to force
# static sample data with no backend running (see web/.env.example)
make web-dev
```

Once all four are running, `/setups` and `/analysis` in the frontend show real
setups computed from the replayed dev dataset. See `web/README.md` for the
frontend's own API layer, environment variables, and test commands.

Check the API:

```bash
curl localhost:8080/healthz
curl localhost:8080/readyz
curl localhost:8080/api/v1/ping
```

Or run everything in containers:

```bash
make up
```

## Development

```bash
make check     # fmt, vet, lint, test  (run before committing)
make generate  # regenerate sqlc and OpenAPI code
```

See `CONTRIBUTING.md` for the workflow and `CLAUDE.md` for the project map.

## Layout

```
cmd/            API and worker entrypoints
internal/       modules: config, httpapi, platform, store, and feature modules
api/            OpenAPI spec and codegen config
db/             migrations and sqlc queries
deploy/         Dockerfiles
web/            React + TypeScript frontend
docs/           ADRs and PRD
.github/        CI and templates
```

## License

Proprietary. See `LICENSE`.
