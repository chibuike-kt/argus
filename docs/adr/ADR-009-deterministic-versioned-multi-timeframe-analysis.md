# ADR-009: Deterministic, versioned, multi-timeframe analysis

- Status: Confirmed direction; exact rules partially open
- Category: Technical-analysis engine
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-010](ADR-010-setup-state-invalidation-and-re-entry.md), [ADR-011](ADR-011-immutable-decisions-corrections-and-plan-revisions.md), [ADR-012](ADR-012-proposed-plans-sizing-and-targets.md), [ADR-019](ADR-019-testing-and-point-in-time-reproducibility.md)

---

## Decision

ARGUS will use a hybrid analysis architecture with a deterministic rules engine as the authority for eligibility and confirmation.

AI may assist with interpretation, explanation and contextual analysis, but it cannot bypass mandatory rules or declare a setup confirmed when required conditions fail.

The engine must model the reasoning process of a technical analyst, not just generate a directional label from indicators.

## Analysis sequence

The intended analytical process is:

- Select an instrument from the active watchlist.

- Establish higher-timeframe trend and market structure.

- Identify eligible supply/demand or support/resistance POIs.

- Track the validity and retests of each POI.

- Evaluate lower-timeframe entry conditions.

- Evaluate the required confluences.

- Construct a proposed entry, stop-loss, take-profit targets and invalidation.

- Evaluate confirmation using closed candles and valid data.

- Track the setup while it remains pending.

- Emit lifecycle events and notify the user about meaningful transitions.

- Reevaluate only the affected dependencies when relevant market data changes.

## Strategy configuration

The initial strategy architecture will use one built-in, configurable, versioned strategy profile.

Each strategy version must explicitly define:

- Context, POI and entry-validation timeframes.

- Swing detection parameters.

- Trend and structure rules.

- POI identification rules.

- Required conditions and their grouping semantics.

- Entry rules.

- Stop and target construction.

- Setup validity and expiry.

- Confirmation and invalidation behavior.

- Material plan-change policy.

- Any applicable re-entry rules.

The exact initial parameter values and condition groups remain open.

## Trend and structure

Trend classification will be based on confirmed market structure and swing sequences.

BOS and CHOCH are close-confirmed. A candle must close beyond the relevant confirmed swing level; a wick-only breach is insufficient.

The exact distinction between BOS and CHOCH, the trend states, and their transition rules must be explicitly specified.

## Swing detection

The initial swing detector will use configurable symmetric fractals.

The default is two candles to the left and two to the right of a candidate pivot.

A pivot must not be treated as known before the required right-side candles have closed. Pivot time and confirmation time must be distinct.

Equal-pivot and tie-handling behavior must be deterministic and versioned.

## Initial POIs

Initial POI categories are:

- Supply and demand zones.

- Support and resistance regions.

Supply/demand zones use a base followed by a qualifying directional impulse. The zone boundaries use the full wick range of the defined base.

Support/resistance is derived from clusters of nearby confirmed swing levels, using ATR-relative tolerance and a minimum of two distinct confirmed swings.

Order blocks and fair value gaps are initially confluence evidence, not independent top-level POI categories, unless a later approved strategy decision changes that scope.

## Displacement

A qualifying displacement requires both an ATR-relative threshold and structural evidence.

ATR uses a default period of 14 and remains configurable. The exact ATR calculation, smoothing, warm-up behavior and displacement thresholds remain open.

## Equal highs and lows

Equal highs and lows are detected through clustering of relevant price levels. Cluster tolerances, merging and minimum-evidence rules must be deterministic.

## Liquidity sweeps

A qualifying sweep requires a breach of the relevant liquidity level followed by a close back within the level's permitted reclaim window.

The reclaim window is configurable and versioned.

A wick beyond a level without the required reclaim behavior is not sufficient.

## Order blocks

The initial order-block convention is the last opposite-direction candle before a qualifying structural break.

Exact eligibility, candle selection and zone-boundary edge cases remain open.

## Fair value gaps

The initial FVG convention is a three-candle, wick-to-wick non-overlap pattern.

A configurable minimum gap size applies.

The precise measurement, rounding and threshold rules remain open.

## POI retests and strength

POI retests are tracked as evidence. The first touch does not automatically invalidate a zone.

Evidence may include departure strength, age, retest count and structural context.

The initial design should expose evidence directly rather than depend on an opaque composite strength score.

## Mandatory conditions

A strategy can define mandatory condition groups. The exact meaning of group logic, such as all conditions, any condition or a minimum count, remains open.

All mandatory requirements must be satisfied for confirmation.

If a required condition cannot be evaluated because data is stale, incomplete or insufficient, ARGUS withholds confirmation.

An unevaluable condition is not automatically a market invalidation.

## Conflicting evidence

Conflicts must be visible rather than silently hidden by a ranking algorithm.

If mandatory higher-timeframe context conflicts with a setup, confirmation is blocked according to the applicable strategy rule.

Every confirmed setup must expose an evidence chain containing the relevant timeframe context, structures, POIs, condition results and source market-data revisions.

## Rule versioning

Rule changes require a new strategy version.

Historical decisions must retain the strategy and configuration version that was used at the time.
