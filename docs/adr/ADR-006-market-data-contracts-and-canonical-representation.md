# ADR-006: Market-data contracts and canonical representation

- Status: Partially confirmed
- Category: Market-data architecture
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-007](ADR-007-provider-selection-and-commercial-data-rights.md), [ADR-011](ADR-011-immutable-decisions-corrections-and-plan-revisions.md), [ADR-019](ADR-019-testing-and-point-in-time-reproducibility.md)

---

## Decision

ARGUS will use a normalized internal market-data contract across supported providers.

The initial data model includes:

- One-minute canonical OHLCV candles.

- Derived candles for 5-minute, 15-minute, 1-hour, 4-hour and 1-day intervals.

- Latest bid/ask quotes.

- Canonical instrument identities.

- Per-market freshness policies.

- Explicit data-quality status.

- Versioned candle corrections.

- Gap detection and recovery.

- Provider-backed historical data.

Candle timestamps will be normalized to UTC, with market-aware rules governing session boundaries and interval construction.

## Candle aggregation

The canonical base interval is one minute.

Higher intervals should be derived consistently from the canonical series, with deterministic boundaries and explicit handling of missing source intervals.

ARGUS must not silently present an aggregate as complete when required source candles are missing.

## Candle states and corrections

The system must distinguish incomplete or provisional observations from finalized candle data where the provider and feed model support that distinction.

Corrected candles create new revisions rather than silently overwriting the historical input used by an earlier decision.

Missing intervals are recorded as gaps. Reconnection alone is not sufficient recovery: the system must attempt to reconcile missing data through backfill and expose unresolved gaps.

## Quote persistence

Only the latest bid/ask quote is selected for the initial market-data model.

Historical quote retention and tick-level replay are not confirmed requirements.

Consequently, ARGUS must not claim accurate historical spread-aware fill simulation where the necessary historical quote evidence was never retained.

## Historical retention

The intended candle-history policy is two years of versioned candle data for the enabled launch universe and required intervals.

Feasibility depends on provider history, storage costs, revision volume and licensing rights.

The treatment of quote history, raw provider messages and long-term revision retention remains open.

## Instrument registry

Every instrument must have a canonical identity and validated metadata, including where applicable:

- Market and provider.

- Venue or data source.

- Symbol mapping.

- Contract type.

- Price and quantity precision.

- Tick and contract conventions.

- Supported intervals.

- Historical coverage.

- Quote availability.

- Enablement state.

An instrument must not be enabled merely because a symbol exists at a provider.

## Market calendar

Freshness policies and gap detection are evaluated against a per-market trading calendar.

Crypto perpetual markets are treated as continuous. Forex markets follow session hours and close on weekends and holidays.

A scheduled market closure is reported as closed. It is not recorded as a data gap and does not raise a stale-feed alert.

The calendar source and holiday handling for each supported market must be defined before that market is enabled.

## Freshness and quality

Freshness targets are market-specific and must be defined numerically before release.

Market-data quality is a first-class state. It must propagate to dependent analysis, setups, chart annotations and notifications.

Stale or incomplete data must block confirmation when the required evidence is not trustworthy.
