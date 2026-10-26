# ADR-019: Testing and point-in-time reproducibility

- Status: Confirmed direction; some acceptance criteria open
- Category: Testing, reproducibility and deployment
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md), [ADR-011](ADR-011-immutable-decisions-corrections-and-plan-revisions.md)

---

## Testing strategy

ARGUS will use layered testing:

- Unit tests.

- Integration tests.

- API contract tests.

- End-to-end tests for critical journeys.

- Data-quality and market-data fixture tests.

- Deterministic strategy replay tests.

- Load and spike tests.

- Security tests.

- Recovery and restore tests.

Critical frontend journeys include sign-in, watchlists, live-data status, setup details, chart interactions and notifications.

## Point-in-time correctness

The engine must prevent look-ahead bias.

A historical evaluation may use only information that would have been available at the evaluation time, including the applicable candle revision and swing-confirmation time.

A pivot cannot be treated as known before the required right-side candles have closed.

## Reproducibility record

A decision must be reproducible from the relevant:

- Evaluation time.

- Strategy and configuration version.

- Input candle revisions.

- Instrument metadata.

- Evidence and condition results.

- Applicable event ordering.

- Plan assumptions.

The exact event and revision retention requirements needed to guarantee reproducibility must be balanced against storage and cost.

## Strategy rollout

A new strategy version uses shadow evaluation before explicit promotion.

Shadow evaluation compares:

- Decision differences.

- Reproducibility.

- Data-quality behavior.

- Latency.

- Incremental resource consumption.

Shadow evaluation must be bounded and must not emit ordinary user-facing confirmations or notifications.

Promotion requires explicit approval after the agreed validation criteria pass.

## Failure tests

The test suite must include:

- Provider outage.

- Stale and missing candles.

- Corrected candles.

- Duplicate events.

- Worker restart.

- Redis loss or interruption.

- WebSocket disconnection and replay.

- Failed notification delivery.

- Database restore.

- Recovery after interrupted backfill.

- Repeated lifecycle evaluation.

- Expiry, invalidation and confirmation collisions.
