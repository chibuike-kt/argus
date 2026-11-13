# ARGUS web

React + TypeScript + Vite frontend. See the repo root `README.md`'s "Quick
start" for the full local stack (Postgres, migrations, seed, worker, API)
this frontend talks to.

## Scripts

```bash
pnpm install     # once, or after pulling a lockfile change
pnpm dev         # dev server, proxies /api to the Go API on :8080
pnpm run typecheck
pnpm run lint
pnpm run test    # vitest
pnpm run build
```

## API layer (`src/api/`)

- `types.ts` -- wire types mirroring `api/openapi.yaml` and
  `internal/setups/types.go`'s `PlanSnapshot` shape exactly.
- `client.ts` -- `getJSON<T>`, the one fetch path every call goes through:
  base URL, timeout, `AbortSignal`, and the shared `{error:{code,message,
  request_id?}}` shape parsed into a typed `ApiClientError`.
- `money.ts` -- exact scaled-integer <-> decimal-string conversion,
  matching `internal/quant`'s own algorithm. Candle/instrument fields
  arrive as pre-formatted decimal strings and never need this; a setup's
  `plan_snapshot.entry`/`.stop`/`targets[].level` arrive as bare scaled
  integers with no scale attached and do.
- `instruments.ts` / `setups.ts` -- one function per real endpoint.
- `hooks.ts` -- small `{data, loading, error}` hooks per resource, no
  external data-fetching library.
- `mappers.ts` -- wire shapes -> this app's existing display types
  (`@/types/domain`), so already-built components keep their props.

## Dev server port is pinned

`vite.config.ts` sets `server.port = 5173` with `strictPort: true`. The
backend's CSRF rule (`ARGUS_AUTH_ALLOWED_ORIGINS`) allows exactly
`http://localhost:5173` and `http://127.0.0.1:5173` by default -- if
something else is already using 5173, `pnpm dev` refuses to start
rather than silently moving to the next free port, which would
otherwise make every POST/PUT/PATCH/DELETE fail with `csrf_rejected`
for a reason that isn't obvious from the error alone.

## Environment variables

See `.env.example`. `VITE_API_BASE_URL` (default a relative `/api/v1`, proxied
by the dev server) and `VITE_USE_FIXTURES` (force every hook to serve
`@/mocks/fixtures`-derived data instead of calling the real API -- offline
dev, screenshots; shows a small indicator bottom-right while on).

## Tests

`pnpm run test` runs vitest against mocked `fetch` only -- no test hits a
real server. `src/api/__tests__` covers the client (success, error-shape
parsing, timeout/abort) and the mapping functions (money handling
especially); each wired route has a loading/empty/error test using a
mocked hook module.

## Running the e2e suite (Playwright, against the real stack)

`web/e2e` runs against a real api+worker pair, the production build (never
the dev server), and a dedicated database -- never the dev `argus` one.
Self-contained and repeatable: each run signs up its own throwaway user
through the real HTTP flow, looks up instrument/setup ids at run time, and
cleans its own session up afterward (see `e2e/global-setup.ts` and
`e2e/global-teardown.ts`'s own doc comments for exactly what "clean up" does
and does not mean here -- there is no account-deletion endpoint, only
session revocation).

From a fresh clone:

```bash
make infra                                            # postgres + redis
make e2e-stack                                        # separate terminal; blocks
cd web && pnpm install
pnpm exec playwright install chromium                 # once
E2E_API_PORT=8090 E2E_API_LOG=/tmp/argus-e2e-api.log pnpm e2e
```

`make e2e-stack` also runs the worker with Part 2G's own live replay
(`ARGUS_DEV_FIXTURE_LIVE_REPLAY=true`) -- new candles forming and closing
forward from each instrument's own last historical close, published through
the hub like a real feed would be. `web/e2e/live-ticks.spec.ts` is the real-
stack proof this enables: a forming candle's own close value visibly changes
over time, and killing the live WebSocket connection outright (Playwright's
own `routeWebSocket`, a real connection to the real server, not a mocked
close event) recovers without a page reload.

One important runner setting: `playwright.config.ts` sets `workers: 1`.
`fullyParallel: false` alone still lets Playwright run different spec
*files* in parallel workers; observed directly in this repo, that starved
two unrelated specs of CPU against the one shared dev-fixture backend and
the one shared session from `global-setup.ts`, each stalling for over 11
minutes on an assertion that normally resolves in under 15 seconds. The
whole suite shares one backend and one session deliberately -- it must run
one test at a time, not fan out.

### Watching it on screen yourself, outside the test suite

`make live-demo` is the one command for that: it seeds the dev fixture
against the dev `argus` database and runs the worker with the same
historical-ingest-plus-live-replay combination, so `pnpm dev`'s own already-
running frontend (pointed at the dev api on :8080) shows real candles moving.
`LIVE_DEMO_SPEED` (env var, default 60) controls the pace -- 60 means about
one new candle every real second.
