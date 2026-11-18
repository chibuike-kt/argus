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

ARGUS runs on real Hyperliquid market data only (Phase 2, D2). There is no
mock or sample mode.

```bash
# 1. deps and env -- copies .env.example to .env, which sets
#    ARGUS_INGEST_PROVIDER=hyperliquid (the worker refuses to start
#    without it) and ARGUS_HYPERLIQUID_SYMBOLS=BTC,ETH,SOL
make setup

# 2. start postgres + redis
make infra

# 3. apply migrations
make migrate-up

# 4. register the Hyperliquid instruments. They are created DISABLED; the
#    command prints the SQL to enable them. Run that SQL yourself, for the
#    symbols you want (nothing in code ever enables an instrument, ADR-007).
make sync-instruments

# 5. run the API (new terminal)
make run-api

# 6. run the worker (new terminal): it backfills the enabled instruments
#    from Hyperliquid, then streams live candles
make run-worker

# 7. frontend dev server (new terminal), wired to the API above
make web-dev
```

Or run the whole backend in Docker with `make up` (the same `.env` supplies
`ARGUS_INGEST_PROVIDER`; without it the worker container exits with a clear
message and postgres, redis and the API still run).

Once running, `/analysis` shows the enabled Hyperliquid instruments with
live candles. See `web/README.md` for the frontend's own API layer,
environment variables, and test commands.

**An existing dev database with the old fixture instruments:** hide them by
hand (nothing in code does this):

```sql
UPDATE instruments SET enabled = FALSE WHERE provider = 'fixture';
```

The fixture dataset survives only for the end-to-end test stack
(`make e2e-stack`, its own `argus_e2e` database, `ARGUS_INGEST_PROVIDER=fixture`
set there explicitly).

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
