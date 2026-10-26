# ADR-011: Immutable decisions, corrections and plan revisions

- Status: Confirmed direction
- Category: Setup lifecycle and event history
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-006](ADR-006-market-data-contracts-and-canonical-representation.md), [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md), [ADR-019](ADR-019-testing-and-point-in-time-reproducibility.md)

---

## Decision

Confirmed decisions and plan versions are immutable historical records.

The current state may change, but earlier decisions must remain reconstructable.

A setup record must preserve, as appropriate:

- Instrument and relevant market-data revisions.

- Evaluation time.

- Strategy and configuration versions.

- POI and structure evidence.

- Condition evaluation results.

- Proposed entry, stop-loss and targets.

- Risk and cost assumptions.

- Confirmation and lifecycle events.

- Relevant data-quality state.

## Corrected market data

When a provider corrects a candle:

- Append a new candle revision.

- Identify affected analysis dependencies.

- Reevaluate affected decisions.

- Preserve the original decision and evidence.

- Append amendment events where results change.

- Update the current-state projection according to the approved rules.

Historical decisions must not be silently rewritten to make them appear as if they had been made using corrected information that was not available at the time.

## Plan revisions

A material plan change creates a new linked plan version.

The original plan remains intact. The new plan records the reason for revision, applicable market-data revisions and supporting evidence.

The exact definition of a material change is strategy-defined and versioned.

Potentially material fields include entry, stop, targets, invalidation, expiry and qualifying evidence. Exact thresholds and rules remain open.

## Dependency-driven reevaluation

Reevaluation should target affected dependencies instead of recalculating every instrument and setup indiscriminately.

The dependency graph and its propagation guarantees must be validated with tests for corrected candles, changed swing confirmations, invalidated POIs and revised setup conditions.
