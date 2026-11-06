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
