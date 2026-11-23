# ADR-006: Market-data contracts and canonical representation

- Status: Partially confirmed
- Category: Market-data architecture
- Version: 0.1.0-draft
- Last updated: 22 November 2026
- Amended: 22 November 2026 (D11, D15.1)
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

Amended 22 November 2026 (D11): 15m, 1h and 4h, and later 1d, are also stored natively from the provider; 5m stays derived from 1m, and derivation from 1m stays as the comparison reference until the native versus aggregated comparison is reported.

ARGUS must not silently present an aggregate as complete when required source candles are missing.

## Higher timeframe candles (amended 22 November 2026, decision D11)

The 1 minute candle remains the canonical live representation. Higher timeframe candles (15m, 1h, 4h
and later 1d) are also stored natively from the provider, backfilled from the provider's historical
endpoint and kept current from its candle stream, because aggregating a short 1 minute history cannot
establish higher timeframe structure. Native and aggregated candles are compared on real overlapping
data before either is relied on. Closed candle finality, revisions and corrections follow the same
rules at every timeframe.

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

Amended 22 November 2026 (decision D15.1): an instrument may also be enabled by one explicit, audited
operation: a user adding a Hyperliquid perpetual to their own watchlist, only outside production,
recorded in instrument_enablements with who, which instrument, when and why. The worker never enables
an instrument, and a symbol existing at a provider still never enables it by itself.

## Market calendar

Freshness policies and gap detection are evaluated against a per-market trading calendar.

Crypto perpetual markets are treated as continuous. Forex markets follow session hours and close on weekends and holidays.

A scheduled market closure is reported as closed. It is not recorded as a data gap and does not raise a stale-feed alert.

The calendar source and holiday handling for each supported market must be defined before that market is enabled.

## Freshness and quality

Freshness targets are market-specific and must be defined numerically before release.

Market-data quality is a first-class state. It must propagate to dependent analysis, setups, chart annotations and notifications.

Stale or incomplete data must block confirmation when the required evidence is not trustworthy.
