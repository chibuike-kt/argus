# ARGUS project status audit

**Date:** 2026-11-06
**`origin/main` commit audited:** `09144fe86cfeff7e515e604549fa51e1182e9983`
**Scope:** whole repo (backend, `web/`, `docs/`). Read-only audit — no code, migration, ADR, spec, or config file was changed. This file and this branch (`docs/project-status-audit`) are the only things added.

## Table of contents

- [A. Backend inventory, package by package](#a-backend-inventory-package-by-package)
- [B. Data layer](#b-data-layer)
- [C. Strategy spec vs code](#c-strategy-spec-vs-code)
- [D. API surface](#d-api-surface)
- [E. Worker and pipeline](#e-worker-and-pipeline)
- [F. Frontend](#f-frontend)
- [G. ADR and appendix conformance](#g-adr-and-appendix-conformance)
- [H. Quality and risk](#h-quality-and-risk)
- [I. Repo hygiene](#i-repo-hygiene)
- [J. Direction](#j-direction)
- [Commands run for this audit](#commands-run-for-this-audit)

---

## A. Backend inventory, package by package

| Package | Purpose | State | Key exports | Tests | Depended on by |
|---|---|---|---|---|---|
| `internal/analysis` | Deterministic swing/market-structure/POI/trade-plan engine (spec sections 1–14) | DONE | `Engine`, `NewEngine`, `Store`, `StateSnapshot`, `ResolveTradePlan`, `ResolvePOI`, `ResolveOrderBlock`, `ResolveStructuralLiquidity`, `CandleSource` | 30 unit + 92 integration | `cmd/argus-worker`, `internal/setups`, `internal/httpapi` (tests only) |
| `internal/clock` | Injected clock + distinct time-semantic types (event/ingestion/processing/evaluation) | DONE | `Clock`, `Real`, `Fixed`, `Mutable`, `EventTime`/`IngestionTime`/`ProcessingTime`/`EvaluationTime` | 9 unit | nearly every other package (foundational) |
| `internal/config` | Env-based startup config, fail-fast validation | DONE | `Config`, `Load()` | 2 unit | `cmd/argus-api`, `cmd/argus-worker` |
| `internal/cursor` | Persists the worker's per-(instrument,timeframe) resume point across `analysis.Engine`+`setups.Reconcile` | DONE, but **no dedicated test file** — `doc.go`+`store.go` only (64 lines), 0 unit/0 integration tests of its own; exercised only indirectly via `cmd/argus-worker`'s own cursor/idempotency integration tests | `Store`, `NewStore`, `Get`, `SetAt`, `ErrNotFound` | 0 (own package) | `cmd/argus-worker` |
| `internal/httpapi` | REST router/middleware wiring + generated strict-server handlers | DONE but small surface: only `/healthz`, `/readyz`, `/api/v1/ping`, `/api/v1/instruments(+/candles)`, `/api/v1/setups(+/{id}+/events)` | `NewRouter`, `apiHandler`, generated `StrictServerInterface` | 19 unit + 2 integration | `cmd/argus-api` |
| `internal/identity` | Named for ADR-003/ADR-015 (auth/authz) | **ABSENT/STUB** — `doc.go` only, 3 lines, zero other code, **zero importers anywhere in the repo** | none | 0 | nothing |
| `internal/ingestion` | Provider-agnostic candle/quote normalization, gap detection, historical backfill | DONE as a package (tested, provider-agnostic) but **FIXTURE-ONLY in practice**: `ingestion.NewService` is called from exactly one place, `cmd/argus-worker/devfixture.go`, always with the fixture provider, gated behind `ARGUS_DEV_FIXTURE_INGEST`; no real exchange/broker `Provider` implementation exists (ADR-007 still open) | `Service`, `NewService`, `Provider`, `InstrumentCache`, `Normalizer` | 32 unit + 5 integration | `cmd/argus-worker` (fixture path only) |
| `internal/ingestion/fixture` | Replays a checked-in recorded dataset as an `ingestion.Provider` | FIXTURE-ONLY by definition | `Provider`, `Data`, `DevDataset()` | 7 unit | `cmd/argus-worker` (devfixture + integration tests) |
| `internal/instruments` | Canonical instrument registry; enablement intentionally cannot be set to TRUE by any code path here (ADR-007 gate) | DONE | `Store`, `Insert`, `GetByID`, `GetByProviderAndSymbol`, `ListEnabled`, `ListAll` | 3 unit + 5 integration | `cmd/argus-worker`, `internal/httpapi`, `internal/ingestion`, `internal/analysis`/`internal/setups` (tests only) |
| `internal/marketdata` | Candle/quote/gap storage, upsert-forming/close/supersede candle lifecycle, market-calendar freshness | DONE | `Store`, `UpsertForming`, `Close`, `SupersedeCandle`, `ListClosedInRangeLimited`, `Candle`, `ContinuousCalendar` | 12 unit + 12 integration | `cmd/argus-worker`, `internal/analysis`, `internal/httpapi`, `internal/ingestion` |
| `internal/notifications` | Named for ADR-013 (notification delivery, WebSocket recovery) | **ABSENT/STUB** — `doc.go` only, 3 lines, **zero importers anywhere** | none | 0 | nothing |
| `internal/platform/postgres` | `*sql.DB` connection opener | DONE (thin) | `Open` | 0 | `cmd/argus-api`, `cmd/argus-worker` |
| `internal/platform/redis` | Redis client opener | DONE (thin), but see section H: wired in, used only for a health-check ping | `Open` | 0 | `cmd/argus-api`, `cmd/argus-worker` |
| `internal/platform/logging` | `slog` logger construction from config | DONE (thin) | `New` | 0 | `cmd/argus-api`, `cmd/argus-worker` |
| `internal/quant` | Money/ratio types: `Price`/`Qty` as scaled `int64`, `Scale`, decimal parsing/formatting, no floats | DONE | `Price`, `Qty`, `Scale`, `ParsePrice`, `ParsePriceRounded`, `Rescale`, `(Price).String(scale)` | 19 unit | nearly every domain package (foundational) |
| `internal/setups` | Persisted setup lifecycle, identity, immutable events, stop-breach invalidation (spec section 15) | DONE, 0 unit tests — all tests are integration-only | `Reconcile`, `InvalidateBreached`, `Store`, `Setup`, `SetupEvent`, `State`/`EventType` enums | 0 unit + 19 integration | `cmd/argus-worker`, `internal/httpapi` |
| `internal/store` | sqlc-generated query code — **generated, must not be hand-edited** (CLAUDE.md rule 4) | DONE (generated) | `Queries`, `New`, `WithTx`, per-table row/param structs | 0 (generated; exercised transitively) | `internal/analysis`, `internal/instruments`, `internal/marketdata`, `internal/setups`, `internal/cursor` |
| `cmd/argus-api` | REST API server entrypoint | DONE but thin: wires config/postgres/redis/logging/httpapi only, no analysis/setups processing lives here | `main` | 0 | — |
| `cmd/argus-worker` | Background processing entrypoint: ticker-driven engine+reconcile+invalidate per enabled instrument, plus the dev-fixture-only ingestion path | DONE for the processing loop; the "market-data ingestion" half of the package's own doc comment is **not wired** (its own `main.go` comment says so explicitly) | `main`, `newProcessor`, `processInstrument`, `reconcileSetup`, `runDevFixtureIngest` | 0 unit + ~8 integration across 3 files | — |

**Notes beyond the table:**
- `internal/identity` and `internal/notifications` are the only two packages that are pure `ABSENT` stubs — each is a 3-line `doc.go` with no other file and zero importers.
- Three packages (`setups`, most of `analysis`, large parts of `marketdata`/`instruments`) have zero unit-test coverage under plain `go test ./...` and are only exercised under `-tags=integration` — meaning the bare unit run alone makes business-critical packages look untested when they are not (see the coverage table in section H).
- The single biggest "smaller than it sounds" finding: the entire pipeline today only ever sees data via `ARGUS_DEV_FIXTURE_INGEST`'s fixture replay, because no real `ingestion.Provider` exists (ADR-007 open) — elaborated in section E.

---

## B. Data layer

**Migrations** (`db/migrations/`, in order):

| # | Adds |
|---|---|
| 000001_init | `instruments` registry table (provider-qualified, enablement gated, `UNIQUE(provider, native_symbol)`) |
| 000002_instrument_sizing_and_scale | Adds `price_scale`, `qty_scale`, `tick_size` (BIGINT), `qty_step_size`, `contract_size`, `pip_size` to `instruments`; trigger making `price_scale`/`qty_scale` immutable after insert |
| 000003_candles_quotes_and_gaps | `candles` (1m OHLCV, forming/closed/superseded lifecycle + `trg_candles_enforce_lifecycle`), `candle_gaps`, `latest_quotes` |
| 000004_drop_instrument_scale_defaults | Drops the `DEFAULT 0` on scale/tick/step columns so an insert must set them explicitly |
| 000005_structural_events | `confirmed_swings` (append-only pivots), `structure_events` (direction-established/BOS/CHOCH), `structure_state` (mutable projection, one row per instrument+timeframe) |
| 000006_liquidity_sweeps_and_protected_structure | `liquidity_sweeps` (append-only); adds `protected_low_swing_id`/`protected_high_swing_id` to `structure_state`; adds `sweep_validated`/`sweep_event_id`/`protected_swing_id` to `structure_events` |
| 000007_structure_events_stable_trigger_key | Adds `trigger_open_time` to `structure_events`, rebuilds the current-row unique index on it instead of `trigger_candle_id` (fixes a correction-replay keying flaw) |
| 000008_setup_lifecycle | `setups` (mutable projection, one row per identity) and `setup_events` (append-only, immutable via trigger) |
| 000009_processing_cursors | `processing_cursors` (mutable projection, worker resume point per instrument+timeframe) |

**Tables, roles, readers/writers:**

| Table | Role | Written by | Read by |
|---|---|---|---|
| `instruments` | Registry | `instruments.Store.Insert` — nothing in this package can ever set `enabled=TRUE`; dev/test seeding bypasses it via raw SQL | `instruments.Store.{GetByID,GetByProviderAndSymbol,ListEnabled,ListAll}`; `httpapi` (`GET /instruments`); worker's `ListEnabled` |
| `candles` | Mutable-until-closed, then append-only-with-supersede | `marketdata.Store.{UpsertForming,Close,SupersedeCandle}` | `marketdata.Store.{GetCurrent,GetLatestClosed,ListClosedInRangeLimited}`; `analysis.MarketdataSource`; `httpapi` (`GET /instruments/{id}/candles`) |
| `candle_gaps` | Append/mutable evidence of missing minutes | `marketdata.Store.{RecordGap,ResolveGap}` | `marketdata.Store.ListOpenGaps` — **BUILT-NOT-WIRED**: unit-tested, zero callers in any `cmd/` entrypoint |
| `latest_quotes` | Mutable projection | `marketdata.Store.UpsertQuote` | `marketdata.Store.GetQuote` — **BUILT-NOT-WIRED**, same finding |
| `confirmed_swings` | Append-only, supersede-handshake | `analysis.Engine` → `Store.{InsertSwing,SupersedeSwing}` | every section 7–14 resolver |
| `structure_events` | Append-only, supersede-handshake | `analysis.Engine` → `Store.{InsertEvent,SupersedeEvent}` | `Store.{GetCurrentEvent,LatestEventBefore,LatestSweepValidatedEventKnownAsOf,LatestInvalidatingChochBefore,...}` |
| `structure_state` | Mutable projection, 1 row/(instrument,timeframe) | `Store.SetState`, every engine step | every resolver |
| `liquidity_sweeps` | Append-only, supersede-handshake | `analysis.Engine` → `Store.{InsertSweep,SupersedeSweep}` | `Store.{GetCurrentSweepByTrigger,GetCurrentSweepBySwept,...}` |
| `setups` | Mutable projection, 1 row/identity | `setups.Store.{Insert,Update}` via `Reconcile`/`InvalidateBreached` | `setups.Store.*`; `httpapi` (`GET /setups`, `GET /setups/{id}`) |
| `setup_events` | Append-only, immutable (UPDATE forbidden by trigger) | `setups.Store.InsertEvent` | `setups.Store.ListEvents`; `httpapi` (`GET /setups/{id}/events`) |
| `processing_cursors` | Mutable projection, 1 row/(instrument,timeframe) | `cursor.Store.SetAt` | `cursor.Store.Get`, worker's `loadCursor` |

**Tables nothing reads or writes:** none at the SQL level. But `candle_gaps` and `latest_quotes` are effectively unused in any running path — real code exists and is unit-tested, but no `cmd/` entrypoint ever calls it.

**Triggers and constraints enforcing invariants:**

| Name | Enforces |
|---|---|
| `trg_instruments_prevent_scale_update` | `price_scale`/`qty_scale` immutable after insert |
| `candles_duration_one_minute`, `candles_open_time_minute_aligned` | the canonical series is strictly 1-minute, UTC-minute-aligned |
| `candles_ohlc_consistent`, `candles_ohlc_nonnegative`, `candles_volume_nonnegative` | basic OHLCV sanity |
| `idx_candles_current` (partial unique) | exactly one current row per `(instrument_id, open_time)` |
| `trg_candles_enforce_lifecycle` | forming→closed→superseded state machine |
| `candle_gaps_resolution_requires_resolved_at` | can't mark a gap resolved without saying how |
| `idx_confirmed_swings_current`, `trg_confirmed_swings_enforce_lifecycle` | one current swing per position; evidence immutable from insert |
| `structure_events_direction_shape` | a BOS keeps direction, a CHOCH flips it exactly, establishment starts from `undetermined` |
| `idx_structure_events_current` (on `trigger_open_time`) | one current event per triggering position, correction-safe (fixed by 000007) |
| `structure_events_sweep_tag_consistency` | `sweep_validated=true` iff both `sweep_event_id` and `protected_swing_id` are set |
| `idx_liquidity_sweeps_current` | one current sweep per `(trigger_open_time, side)` |
| `setups` CHECK constraints | `poi_type`/`direction`/`state` restricted to the spec's own vocabulary |
| `idx_setups_identity` (unique) | one row per full identity tuple (spec section 15.3) |
| `trg_setup_events_prevent_update` | `setup_events` rows can never be updated, pure append-only |

---

## C. Strategy spec vs code

The spec (`docs/strategy/strategy-spec.md`, version 0.12.1 as audited) and the engine code are
unusually well-aligned for sections 7, 14 and 15 specifically because this session's own prior work
(three briefs across this same engagement) wrote the spec text and the implementing code together,
with tests pinning the exact behavior described. Sections 1–6 and 8–13 predate that work; this audit
verified their wiring and test existence directly but did not re-run a full line-by-line spec-text
diff for them the way it did for 7/14/15 — flagged `INFERRED (lighter verification)` where that applies.

| § | Topic | Implemented | Where | Tests exist | Code matches spec text |
|---|---|---|---|---|---|
| 1 | Swing detection | Yes | `internal/analysis/detect.go` (`pivotWindow`, `detectPivot`), `engine.go` (`applyPivotStep`, `reconcileSwing`) | Yes | Yes. 2/2 fractal, strict inequality (ties are not a pivot). `LeftBars`/`RightBars` hardcoded to 2/2 at the one real call site, matching spec's `[R]` tag. `INFERRED (lighter verification)` on subtler edges. |
| 2 | Structural direction | Yes | `engine.go` (`decideEstablishmentCandidate`), `state.go` | Yes | `INFERRED (lighter verification)`. |
| 3 | Break classification | Yes | `event.go` (`decideBreak`) | Yes | Matches: BOS continues, CHOCH flips, strict close-beyond, `int64` comparison. |
| 4 | Break threshold | Yes | `event.go` (`decideBreak`) | Yes | Matches `[R]`: strict close-beyond, no buffer. The ATR buffer the spec defers is genuinely absent — no ATR code anywhere. |
| 5 | Determinism and evidence | Yes, as an architecture | Cross-cutting — every append-only table carries `algorithm_version`, pivot/source-candle refs, `evaluation_time`, direction before/after (confirmed against the migration schemas, section B) | Yes (correction-replay tests) | Matches. No-look-ahead enforced by `asOf`/`close_time` discipline throughout every resolver. |
| 6 | Liquidity sweeps | Yes | `sweep.go`, `engine.go` (`applySweepStep`, `reconcileSweep`) | Yes | `INFERRED (lighter verification)` on the exact encoding; confirmed wired and append-only. |
| 7 | Protected structure and sweep-validated breaks | Yes | `engine.go` (`reconcileProtectedAnchor`, `decideSweepValidation`, `applyCloseThroughInvalidation`, `closedThroughAnchorBefore`, `invalidateProtectedAnchor`) | Yes, extensively (5 golden fixtures) | **Directly verified both ways.** Close-through invalidation `[S]` is the primary rule; CHOCH-coupling `[R]` is retained as a documented discrepancy. Proven by running the tests, not assumed. |
| 8 | Premium, discount, equilibrium | Yes | `premium_discount.go` (`ResolveActiveRange`) | Yes | `INFERRED (lighter verification)`. Wired live via `poi.go`. |
| 9 | Structural liquidity | Yes | `structural_liquidity.go` (`ResolveStructuralLiquidity`, `ResolveSecondFib`) | Yes | `INFERRED (lighter verification)`. Wired live via `poi.go`/`trade_plan.go`. |
| 10 | Transactional liquidity | Yes | `transactional_liquidity.go` | Yes | `INFERRED (lighter verification)`. Confirmed wired: `trade_plan.go` calls it directly for target sourcing. |
| 11 | Inducement liquidity | Yes, **never called from the live chain** | `inducement.go` (`ResolveInducementLiquidity`) | Yes | **Not a gap — matches the spec's own explicit deferral.** The spec text says it itself: "The expansion anticipation and the no-trade gate are setup and entry logic, deferred." State: **BUILT-NOT-WIRED, by design.** |
| 12 | Order block | Yes | `order_block.go` (`ResolveOrderBlock`) | Yes | `INFERRED (lighter verification)`. Wired live via `poi.go`. |
| 13 | POI: QMR and selection | Yes | `poi.go` (`ResolvePOI`) | Yes | Matches the spec's own two-type model exactly — but see section G: this **contradicts ADR-009**, which names supply/demand and support/resistance as initial POI categories. Spec and code agree with each other; the ADR disagrees with both. |
| 14 | Trade plan (risk entry) | Yes | `trade_plan.go` (`ResolveTradePlan`) | Yes, extensively | **Directly verified both ways.** Entry must be strictly beyond stop or the plan is `undefined`. A genuine residual case still fires where it should (a stale anchor the market only ever wicks near, never closes through either way). |
| 15 | Setup lifecycle, identity, persistence | Yes | `internal/setups` (`Reconcile`, `InvalidateBreached`) | Yes, extensively | **Directly verified both ways**, across creation-state (a setup created mid-status records `detected` then the mapped state in the same step) and stop-breach invalidation (a persisted setup is invalidated against its own snapshot's stop even after the live anchor is cleared). |

**Choices the code makes that the spec does not state (verified):**
- `LeftBars`/`RightBars` = 2/2 hardcoded at the one real call site, not configurable in practice despite being a struct field.
- One `algorithmVersion` string serves as both `strategy_version` (identity) and `algorithm_version` (evidence) — documented as deliberate until a real distinction is needed.

**Spec rules the code does not implement (verified absent):**
- `Expired`/`Cancelled` setup states — the enum has exactly 4 values.
- Re-evaluating `actionable`/`triggered` for a setup whose identity is no longer the current POI (only stop-breach invalidation reaches back for a superseded setup).
- Mandatory-condition grouping, confirmation entry type, multi-timeframe — explicitly deferred in the spec, confirmed absent in code.

**`[U]`/`[A]` items silently decided by code:** none found. The one borderline case (inducement liquidity) is explicitly, textually deferred by the spec itself, not a silent decision.

**Live vs library-only:** sections 1–10 and 12–15 all execute on every closed candle in the real worker process. **Section 11 is the one exception** — fully implemented and tested, but nothing in the live path ever calls it, exactly as the spec anticipates.

---

## D. API surface

**Mounted routes** (`internal/httpapi/server.go` + generated `openapi_gen.go`):

| Method | Path | Handler |
|---|---|---|
| GET | `/healthz` | `healthHandler.live` |
| GET | `/readyz` | `healthHandler.ready` |
| GET | `/api/v1/ping` | `apiHandler.Ping` |
| GET | `/api/v1/instruments` | `apiHandler.ListInstruments` |
| GET | `/api/v1/instruments/{id}/candles` | `apiHandler.ListInstrumentCandles` |
| GET | `/api/v1/setups` | `apiHandler.ListSetups` |
| GET | `/api/v1/setups/{id}` | `apiHandler.GetSetup` |
| GET | `/api/v1/setups/{id}/events` | `apiHandler.ListSetupEvents` |

8 routes total, all GET — no mutating endpoint exists anywhere.

**Drift vs `api/openapi.yaml`:** none. The same 6 `/api/v1` paths are listed in the spec; `/healthz`/`/readyz` are a documented exception (deliberately outside the spec, at the root). The OpenAPI-first pipeline is faithfully followed both directions.

**Auth: absent, not partial.** Only four middlewares are wired: `RequestID`, a custom request logger, `Recoverer`, `Timeout(30s)`. No authentication/authorization code exists anywhere in `internal/httpapi` (grepped `auth`/`jwt`/`token`/`Bearer`, nothing). Every route, including all setup and instrument data, is unconditionally open. There is no concept of a "user" anywhere in this API. ADR-015 is **NOT STARTED** at the HTTP layer, as a plain fact of the code.

**Pagination:**
- `GET /setups` — `limit` default 50 / max 200, Go-validated then SQL-`LIMIT`-enforced.
- `GET /instruments/{id}/candles` — `limit` default 500 / max 1000, same pattern.
- `GET /instruments` — **no pagination at all**; returns every row unconditionally. The one list endpoint with no cap either side.

**Error shape: fully consistent, no leakage found.** Every 4xx and the global error hooks route through one `apiError`/`writeAPIError` pair, always the same `{error: {code, message, request_id?}}` shape. The 500 path logs the real error server-side but returns only the generic string `"an internal error occurred"` — no raw Go error text, no SQL error, no internal struct ever reaches the response body.

---

## E. Worker and pipeline

### One candle, provider to `setup_events`

Traced directly from source, function by function, for the only path that exists today (the dev-fixture replay — there is no other provider):

1. `internal/ingestion/fixture.Provider.HistoricalCandles` — raw `ProviderCandle` values (decimal strings) from the checked-in embedded dataset.
2. `ingestion.Service.IngestHistorical` → `Normalizer.NormalizeCandle` (decimal strings → `quant.Price`/`quant.Qty`) → `Service.applyCandle`/`closeCandle`.
3. `marketdata.Store.UpsertForming` then `Close` — writes the `candles` row.
4. **Worker tick** (`cmd/argus-worker/processing.go`, `processor.run` → `processInstrument`): `analysis.CandleSource.ListCandles` reads new closed candles since the persisted cursor.
5. Per candle, inside `analysis.Engine.OnCandleClosed` → `step`:
   - `applyPivotStep` → `reconcileSwing` (`confirmed_swings`) → `reconcileProtectedAnchor` (section 7 anchor bookkeeping).
   - `applySweepStep` → `reconcileSweep` (`liquidity_sweeps`).
   - `applyBreakStep` → `applyCloseThroughInvalidation` (v0.12.0, may clear the live protected anchor) → `decideBreak`/`decideEstablishmentCandidate` → `decideSweepValidation` → writes/supersedes `structure_events` → updates `structure_state`.
6. Still in the same per-candle loop, after the engine step commits: `processor.reconcileSetup` → `analysis.ResolveTradePlan` (chains through `ResolvePOI`, `ResolveOrderBlock`/QMR, `ResolveStructuralLiquidity`, `ResolveSecondFib`, `ResolveActiveRange`, `ResolveTransactionalLiquidity` — all pure, no persistence of their own) → `setups.Reconcile` → `reconcileNew`/`reconcileExisting` → writes `setups` + `setup_events`.
7. Still in the same loop: `setups.InvalidateBreached` → `ListNonTerminalForBreachCheck` → for any non-terminal setup whose own recorded stop is breached by this candle's close → `invalidateOne` → writes `setups` + `setup_events` — the step that exists specifically because step 6's live plan alone can go to `PlanNone` on the exact candle that breaches a setup's stop.
8. Cursor advance: `cursor.Store.SetAt` → `processing_cursors`, only after steps 5–7 have all committed.

### What runs automatically on startup

`cmd/argus-worker/main.go`: connect Postgres/Redis, construct the processor, **optionally** (only if `ARGUS_DEV_FIXTURE_INGEST` is set) replay the entire embedded dataset once with an explicit "never run against production" warning — then a 1-minute ticker calls `processor.run(ctx)` forever. That is the entire automatic behavior. Nothing else ever runs automatically.

### What needs flags or seeding

- `ARGUS_DEV_FIXTURE_INGEST=true` — the one-time dataset replay above. Without it, `processor.run` does nothing, because nothing else ever inserts a new candle.
- `db/seed/dev_fixture_instruments.sql` — the only way any instrument is ever enabled today, via a raw-SQL bypass; no code path enables an instrument as part of normal operation.

### Dev-only pieces

`ARGUS_DEV_FIXTURE_INGEST`, `internal/ingestion/fixture` (the whole package), `db/seed/dev_fixture_instruments.sql`, the embedded dataset JSON, and the direct-SQL enablement bypass. None of this is relevant to a real deployment.

### Restart and crash behavior

Confirmed by direct test execution this session (`TestIntegration_Cursor_CrashBetweenEngineAndReconcile_Recovers`): the cursor only advances after the engine step, `reconcileSetup`, and `InvalidateBreached` have all committed for a candle. A restart, or a crash anywhere among those three steps, reprocesses nothing already committed, and resumes from exactly the last successfully-completed candle. Proven idempotent at full-dataset scale by `TestIntegration_IdempotentFullRun_RerunAfterCursorWipeProducesNoChange` (passing, full dataset replayed twice).

### Two worker processes running at once — unguarded, untested

No advisory lock, `SELECT ... FOR UPDATE`, or leader-election exists anywhere (`internal/cursor`, `db/queries/processing_cursors.sql`, `cmd/argus-worker/*.go` all grepped directly — nothing). `processing_cursors` has only a plain primary key and a last-write-wins upsert. Two workers would both call `ListEnabled`/`processInstrument` for the same instruments concurrently. The evidence tables' own unique indexes would likely turn a genuine race into a Postgres constraint error on one writer (not silent corruption), recovered the same way a crash is — but this is reasoned from the schema, **not proven by any test**. Labelled `INFERRED (plausible, unverified)`; the absence of any lock at all is directly verified.

### Known gaps vs. a real live deployment

1. No real market-data provider exists (ADR-007 open) — the pipeline only ever runs against the fixture dataset.
2. No automatic enablement path — every instrument ever processed got there via a direct SQL bypass.
3. No queue-backed ingestion (ADR-005's Redis-queue half) — a plain ticker polls `ListEnabled` directly.
4. No multi-worker safety net (above).
5. No metrics/alerting surface — a stalled worker is only visible in logs.

---

## F. Frontend

**Tooling results:** `pnpm install --frozen-lockfile` clean; `pnpm run typecheck` clean; `pnpm run lint` clean; `pnpm run test` — 5 test files, 62 tests, all passed; `pnpm run build` succeeded in 22.2s, main JS chunk 801.58 kB / 241.40 kB gzip (over Vite's 500 kB warning threshold), CSS 52.28 kB / 9.23 kB gzip.

### Routes

| Route | Shows | Data source | Endpoint it could use instead |
|---|---|---|---|
| `/setups` | Filterable table of findings (instrument, direction, POI, entry/stop/R:R, state, age) | **100% fixtures** (`@/mocks/fixtures`) | `GET /setups` |
| `/analysis` | Full workspace: chart, toolbar, drawing tools, "other setups" strip, right panel | **100% fixtures**, transitively, across every data-bearing child component | `GET /instruments/{id}/candles` (chart), `GET /instruments` (markets/watchlist), `GET /setups/{id}` + `/events` (setup/analysis panel) |
| `/style` | Design-system component showcase | N/A — static demo | N/A |

A repo-wide grep for `fetch(`, `/api/v1`, and `axios` across `web/src` returns exactly one hit, and it is a comment, not code — `src/mocks/fixtures.ts`'s own note that it's a placeholder for when "the endpoints land." **There is no real API call anywhere in the frontend.** All three backend list/detail endpoint groups that exist today are unused by the UI.

### Unused components

Two components are defined but never imported anywhere else in `web/src` (verified by grepping each export name across the whole tree):
- `components/workspace/replay-bar.tsx` → `ReplayBar`
- `components/layout/as-of-control.tsx` → `AsOfControl`

### Accessibility / reduced-motion gaps

- `routes/setups.tsx` — the table that's the default landing route, with clickable rows and a filter-button group acting as a tab bar — has **zero** accessibility attributes: no `role="tablist"`/`aria-selected`, no `aria-label` on the row links.
- `theme.css` implements a real `@media (prefers-reduced-motion: reduce)` guard, but only for three named classes. 15 files use raw Tailwind transition utilities (e.g. `transition-colors`) **not** covered by that guard — those still animate under reduced-motion.

---

## G. ADR and appendix conformance

### ADR-by-ADR

| ADR | Status line | What the code does | Verdict |
|---|---|---|---|
| ADR-001 Product scope and execution boundary | "Confirmed" | No order-placement code anywhere; only persisted analytical plans | HONOURED |
| ADR-002 Deployment model, audience, scale | "Confirmed direction; feasibility validation required" | No load testing exists; `deploy/` has only two Dockerfiles, consistent with a single-host shape | PARTIAL |
| ADR-003 Backend architecture and service boundaries | "Confirmed direction; detailed boundaries partially open" | Modular `internal/` feature packages, separate API/worker binaries — matches | HONOURED |
| ADR-004 API and frontend architecture | "Confirmed direction; implementation details partially open" | REST/OpenAPI/sqlc side fully confirmed; **WebSockets absent** | PARTIAL |
| ADR-005 Postgres, Redis, background processing | "Confirmed direction" | Postgres fully real; Redis wired but used only for a health-check ping — **no Redis-backed queue exists**; idempotency comes from the Postgres cursor table instead | PARTIAL |
| ADR-006 Market-data contracts | "Partially confirmed" | 1m candles/revisions/registry/crypto-calendar honoured; derived intervals and the forex calendar are explicit, honest stubs (`ErrForexCalendarNotImplemented`), not silently wrong | PARTIAL |
| ADR-007 Provider selection and commercial data rights | "Open; research and validation required" | No real provider integrated at all — matches the ADR's own "Open" exactly | NOT STARTED |
| ADR-008 Charts and drawing tools | "Option A decided; phase 2 implemented" | Matches — the one ADR in the set the authors keep current with the code | HONOURED |
| ADR-009 Deterministic, versioned, multi-timeframe analysis | "Confirmed direction; exact rules partially open" | BOS/CHOCH and fractal-swing rules honoured. **Direct contradiction:** the ADR names supply/demand and support/resistance as the initial POI categories and calls order blocks confluence evidence only; the shipped code's entire POI system is order-block/QMR only, with no supply/demand or S&R type anywhere | CONTRADICTED (POI category) / NOT STARTED (ATR, FVG, condition grouping) |
| ADR-010 Setup state, invalidation, re-entry | "Partially confirmed" | States collapsed to 4 (not the ADR's 6), but this is documented in the spec itself with reasons; precedence and re-entry are actually fully implemented and tested despite the ADR text not reflecting it | PARTIAL |
| ADR-011 Immutable decisions, corrections, plan revisions | "Confirmed direction" | Append-only events and correction-replay honoured and extensively tested this session; a distinct "plan revision" concept (vs. a lifecycle transition) doesn't exist | PARTIAL |
| ADR-012 Proposed plans, sizing, targets | "Partially confirmed" | Entry/stop/targets/R:R implemented deterministically; stop has no volatility buffer (explicitly deferred in spec text); position sizing and cost estimation are absent | PARTIAL |
| ADR-013 Notification delivery and WebSocket recovery | "Confirmed direction; implementation details partially open" | `internal/notifications` is a one-line stub; no outbox, channels, or WebSocket code anywhere | NOT STARTED |
| ADR-014 Paper-trading simulation | "Confirmed direction; launch timing open" | No paper-trading code exists | NOT STARTED |
| ADR-015 Authentication and authorization | "Confirmed direction; security parameters partially open" | No auth code, no `users` table, no ownership column anywhere; the API has no auth middleware at all | NOT STARTED |
| ADR-016 Data retention, deletion, privacy | "Confirmed direction; retention periods partially open" | No retention/export/deletion code exists (no account concept exists at all) | NOT STARTED |
| ADR-017 Availability, backups, recovery objectives | "Targets confirmed; feasibility validation required" | No backup automation, PITR config, or restore test exists | NOT STARTED |
| ADR-018 Observability and load testing | "Confirmed direction; numerical thresholds open" | Structured logs and health checks honoured, extensively; no metrics surface, alerting, or load testing exists | PARTIAL |
| ADR-019 Testing and point-in-time reproducibility | "Confirmed direction; some acceptance criteria open" | Point-in-time correctness is the single most heavily tested property in the codebase; shadow evaluation for a new strategy version before promotion is absent | PARTIAL |
| ADR-020 Database migrations and release management | "Confirmed direction; migration tool selected" | golang-migrate honoured; CI runs backend fmt/vet/lint/integration-test and a frontend lint/typecheck/build job, but the **frontend CI job never runs `pnpm run test`** despite 62 passing vitest tests existing locally; no API-contract-diff check exists | PARTIAL |

### Appendix-C conformance

| Item (priority) | Still open? | Evidence | Blocking? |
|---|---|---|---|
| Crypto/forex providers (Critical) | Yes | ADR-007 itself still says "Open"; no real adapter exists | **Yes — the single biggest blocker in the repo** |
| Commercial data rights (Critical) | Yes | No rights-review evidence anywhere | Yes, same blocker |
| $50 all-in feasibility (Critical) | Yes | No costed plan found | Yes, gates the provider decision |
| Charting library and drawing APIs (Critical) | **STALE — already resolved** | ADR-008 itself now says "Option A... phase 2 implemented," with a working implementation | No longer blocking; appendix-c should be updated |
| Initial strategy conditions (Critical) | Partially resolved | Spec sections 1–15 fully specify order-block/QMR with tests; ATR, FVG, equal-highs/lows clustering, condition grouping are absent | Blocks extending the strategy beyond order-block/QMR |
| Event precedence (Critical) | **STALE — already resolved** | Spec section 14/15.2, implemented in `resolvePlanStatus`, tested extensively | Not blocking |
| Swing tie policy (High) | **STALE — already resolved** | `detectPivot`'s strict-inequality rule deterministically excludes ties | Not blocking |
| Base and impulse thresholds / ATR and FVG thresholds (High) | Yes | No ATR/base/impulse/FVG code exists | Moot until/unless a new POI type is added |
| Trend and timeframe mapping (High) | Yes | Single-timeframe only | Blocks genuine multi-timeframe analysis |
| Material plan changes (High) | Yes | No "plan revision" concept exists | Low — current model works without it |
| Re-entry and candidate identity (High) | **STALE — already resolved** | Spec section 15.3's identity tuple, implemented and tested | Not blocking |
| Instrument-aware sizing (High) | Yes | No position-sizing code exists | Blocks any user-facing risk/sizing display |
| Freshness and latency thresholds (High) | Yes | A freshness *mechanism* exists, no per-market numeric SLO is set | Blocks a real alerting story |
| Load-test acceptance (High) | Yes | No load tests exist | Blocks public-launch readiness |
| Backup strategy (High) | Yes | No backup/restore code exists | Blocks any real recovery-objective claim |
| Queue library (Medium) | Yes | Plain ticker, no Redis-backed queue | Blocks multi-worker/partitioned scaling |
| Session/cookie parameters (Medium) | Yes (moot — no auth exists) | No auth code exists | Blocks any auth work |
| Retention periods (Medium) | Yes | No retention jobs exist | Low near-term |
| Replay cursor / snapshot protocol (Medium) | Yes | No WebSocket code exists | Blocks realtime UI work |
| Paper-trading launch timing (Medium) | Yes | No paper-trading code exists | Low near-term |
| Shadow promotion criteria (Medium) | Yes | No shadow-evaluation mechanism exists | Low near-term |
| Export and deletion details (Medium) | Yes | No export/deletion code exists | Low near-term |

---

## H. Quality and risk

**1. Test gaps**

| Package | Unit cover | Integration tests? | Verdict |
|---|---|---|---|
| `cmd/argus-api` | 0.0% | none found | **Real gap** — `main.go` (config load, DB/Redis open, router wiring, graceful shutdown) has zero test coverage of any kind |
| `cmd/argus-worker` | 0.0% | `cursor_integration_test.go`, `idempotent_full_run_integration_test.go`, `pipeline_integration_test.go` | Not a gap — covered, just not by the plain unit suite |
| `internal/cursor` | 0.0% | **none, no `*_test.go` file in the package at all** | `Get`/`SetAt`/`WithTx` have no package-local test; exercised only transitively through the worker's own tests |
| `internal/setups` | 0.0% | `reconcile_integration_test.go`, `invalidate_breached_integration_test.go` | Not a gap — substantial integration coverage |
| `internal/platform/postgres`/`redis`/`logging` | 0.0% | none | Each a ~25-35 line connection wrapper; untested but low-risk in isolation — the real risk is in untested `cmd/*/main.go` wiring |
| `internal/identity`/`internal/notifications` | no test files | n/a | Not a test gap — both are a bare 3-line `doc.go`, nothing exists to test |
| `internal/httpapi` | 37.5% | `setups_integration_test.go`, `instruments_integration_test.go` | Health-check handlers and the request-logger middleware have no dedicated test — minor |

**2. Flaky or slow tests.** No `time.Sleep`/retry-based timing dependency found in any test file. Known slow tests (full embedded-dataset replays): `TestIntegration_IdempotentFullRun_RerunAfterCursorWipeProducesNoChange` (~150-280s depending on run) and `TestIntegration_Pipeline_DevFixtureIngestThroughSetupsLifecycle` (~80-95s). No test file names reference "flaky" or "runaway."

**3. Dependency risk.** `go.mod`: 7 direct deps, all mainstream and actively maintained (chi v5.3.0, go-redis v9.7.3, lib/pq, caarlos0/env, joho/godotenv, oapi-codegen/runtime, getkin/kin-openapi), Go 1.26.10. One recent dependency-bump commit found, nothing added hastily. `web/package.json`: React 18, react-router 7, lightweight-charts, Radix UI, Tailwind 4 — all current, well-known. No unmaintained or unusual packages found.

**4. Secrets/config.** `internal/config/config.go` loads from env via `caarlos0/env`; `.env.example` has only non-secret placeholder values; `.gitignore` correctly excludes `.env`/`.env.*`. Grep for hardcoded secret-looking strings across Go/TS/YAML found nothing. Clean.

**5. Error-swallowing.** All `_ = ...` discards are the correct idiom (`tx.Rollback()` after commit, `Close()` in shutdown defers) or generated code. No silent `if err != nil { return nil }` pattern found anywhere. Clean.

**6. TODO/FIXME/HACK inventory.** One grep hit total, and it's fixture *news-headline text*, not a code marker. **Zero real debt markers in the entire repo.**

**7. `float64` for money/ratios.** Zero real hits. All 5 grep matches in non-test Go source are comments explaining why it's *not* used. Clean.

**8. Other CLAUDE.md rule checks.** One `time.Now()` outside `clock.Real`/tests, in `internal/httpapi/middleware.go`, used only for request-duration logging — a correctly-scoped exception, not business logic. Redis is wired into both entrypoints but used only for the `/healthz` ping, despite ADR-005 describing a queueing role for it — a completeness signal, not a rule violation.

---

## I. Repo hygiene

**1. Branches not merged to `main`:**

| Branch | Finding |
|---|---|
| `docs/spec-v0.7.2` (local + remote) | Content superseded by the current spec (now v0.12.1) — stale ghost, not pending work |
| `feat/web-chart-drawing-engine` (local + remote) | All 3 commits already on `main` under different hashes (rebased/cherry-picked in, branch never deleted) |
| `feat/web-design-system-v2` (local + remote) | A superset of the above plus 2 more commits, also already on `main` under different hashes |
| `origin/feat/analysis-structure-event-key` (remote only) | Same fix, same message, already on `main` as a different commit |

**All four "unmerged" branches are false positives — every one is fully absorbed into `main` already**, just under different commit hashes from a rebase/squash workflow. None represent real pending work. Not deleted, per instructions.

**2. Stale stashes** — 3 entries, all from branches whose work has since finished and whose own messages say the content was captured elsewhere.

**3. Duplicated work.** Confirmed above (`feat/web-design-system-v2` duplicates `feat/web-chart-drawing-engine`'s own commits); no other duplication found between active branches.

**4. Accidentally committed files.** Largest tracked file is the intentional `//go:embed`-ed dev dataset (~756KB). No `dist/`, no `node_modules/`, no build output tracked. Nothing over 1MB. Clean.

---

## J. Direction

### J.1 Next 10 pieces of work, dependency-ordered

| # | What | Why it's next | Depends on | Unblocks | Size | Human decision needed first? |
|---|---|---|---|---|---|---|
| 1 | Wire `web/src/routes/setups.tsx` and `analysis-preview.tsx` to the real `GET /setups`, `GET /instruments`, `GET /instruments/{id}/candles` endpoints, replacing `@/mocks/fixtures` | The backend has three working, tested, OpenAPI-documented GET endpoints the frontend has never called — pure payoff sitting unclaimed | Nothing | A real end-to-end demo of the pipeline already built | M | No |
| 2 | Add a lock or leader-election around per-instrument processing in `cmd/argus-worker` | Verified gap: nothing prevents two worker processes from racing on the same instrument today | Nothing | Safely running more than one worker process | S-M | No |
| 3 | **Decide ADR-007**: provider, commercial rights, budget | The single blocking decision for everything "real" in the project | Nothing (research/negotiation) | Everything below | — | **Yes — provider choice, rights, budget** |
| 4 | Implement a real `ingestion.Provider` adapter for whatever #3 picks | The interface/Service/Normalizer/Cache already exist and are tested; only a new adapter is needed | #3 | Real candle data flowing in for the first time | M-L | No (once #3 is decided) |
| 5 | Decide and implement a real instrument-enablement path | Today only a raw-SQL bypass exists; no production mechanism | #3 | A real instrument actually being processed | S-M | **Yes — who/what enables an instrument** |
| 6 | Wire live ingestion into `cmd/argus-worker/main.go`'s startup | The processing loop itself needs no changes | #3, #4, #5 | The full pipeline running on real data | M | No |
| 7 | Decide and implement baseline API authentication | ADR-015 is NOT STARTED; every route is unauthenticated today | Nothing | Safe to deploy anywhere beyond a laptop | M-L | **Yes — auth model** |
| 8 | Add a `/metrics` endpoint and basic worker-lag alerting | The lag numbers are already computed and logged, never exposed to anything that can alert | Nothing | Knowing the worker has stalled without reading logs | M | **Yes — metrics backend choice** |
| 9 | Resolve the ADR-009 POI-category contradiction | A verified CONTRADICTED finding — the ADR and the shipped spec/code disagree about what a POI is | Nothing | A spec/ADR that agree with each other | S | **Yes — which side is now correct** |
| 10 | Decide and implement position sizing and the ADR-012 volatility stop buffer together | Both are explicitly deferred "later knobs," both still absent; doing one without the other half-finishes the risk picture | Nothing structurally | A trade plan a user could actually size from | M | **Yes — sizing model and buffer timing** |

Smaller, no-decision-needed cleanup to interleave: `GET /instruments` pagination cap, frontend bundle code-splitting (the 801.58 kB chunk), adding `pnpm run test` to frontend CI, deleting the four confirmed-merged "ghost" branches and three stale stashes, and updating appendix-c's four stale "already resolved" entries.

### J.2 Three biggest risks

1. **No real data source, and no committed timeline to get one.** ADR-007 is still "Open" with zero visible progress. Every other finding in this audit is built waiting on this one decision.
2. **A fully open, unauthenticated REST API.** No auth of any kind exists. Not a future risk — the exact state of the code today; the only reason it isn't already an incident is that nothing is deployed anywhere reachable.
3. **The frontend and backend have quietly stopped talking to each other.** Zero `fetch`/API calls exist anywhere in the frontend. Three working backend endpoints have no caller, and nothing — no integration test, no CI gate — would catch the gap growing further.

### J.3 What to cut, defer, or stop

- **Defer** paper trading, notification delivery, retention/deletion, multi-timeframe — none matter until real data exists.
- **Stop** carrying `internal/identity`/`internal/notifications` as empty stub packages; either remove them or don't let them imply progress that hasn't started.
- **Don't** wire `ResolveInducementLiquidity` into the live chain ad hoc — it's correct and tested exactly as far as the spec currently takes it; the spec's own "setup and entry logic" deferral is an open decision, not something to guess at.
- **Bundle** the ADR-012 stop-buffer decision with position sizing rather than doing either alone.

### J.4 Shortest path to a real user seeing real, non-fixture setups on a screen

1. Wire the frontend to the existing API (J.1 #1) — **not blocked by anything**. Proves the pipeline end to end against today's fixture-seeded database.
2. Decide ADR-007 (J.1 #3) — **blocked on a human/architect decision**, not more code.
3. Implement the real provider adapter (J.1 #4) — blocked on step 2.
4. Decide and implement real instrument enablement (J.1 #5) — blocked on step 2, plus its own small decision.
5. Wire live ingestion into the worker's startup (J.1 #6) — blocked on steps 3 and 4.
6. The worker now processes real candles through the exact same pipeline traced in section E — no further backend change needed.
7. Because step 1 already wired the frontend, the UI now shows real, non-fixture setups with **zero additional frontend work**.

The only genuinely blocking step is step 2. Step 1 is listed first precisely because it has no dependency at all — doing it now means the path from "ADR-007 resolves" to "a real user sees it" is backend-only work.

---

## Commands run for this audit

```
git fetch origin main
git checkout main && git merge --ff-only origin/main
git checkout -b docs/project-status-audit

go build ./...
go vet ./...
go vet -tags=integration ./...
golangci-lint run --timeout=5m ./...
go test ./... -cover
go test -tags=integration ./... -cover

cd web
pnpm install --frozen-lockfile
pnpm run typecheck
pnpm run lint
pnpm run test
pnpm run build

git fetch --all
git branch -a --no-merged main
git stash list
find . -type f -size +1M -not -path "*/node_modules/*" -not -path "*/.git/*"
```

All passed/were clean except where noted above (frontend bundle-size warning; otherwise zero failures across build/vet/lint/unit/integration/web checks).

Everything the brief's own priority order could not fit within budget: **none** — all sections A–J were completed within budget for this audit.
