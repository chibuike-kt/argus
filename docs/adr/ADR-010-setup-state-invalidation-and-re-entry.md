# ADR-010: Setup state, invalidation and re-entry

- Status: Partially confirmed
- Category: Setup lifecycle and event history
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md), [ADR-011](ADR-011-immutable-decisions-corrections-and-plan-revisions.md), [ADR-012](ADR-012-proposed-plans-sizing-and-targets.md)

---

## Decision

ARGUS will model a full setup lifecycle, with separate representations for:

- Setup lifecycle.

- Condition-evaluation status.

- Market-data quality.

These concepts must not be collapsed into a single status field.

## Candidate and setup behavior

A candidate may be created before confirmation.

The system continuously evaluates eligible instruments and POIs. Candidate creation, confirmation and visibility must follow versioned strategy rules.

A setup may remain pending while awaiting confirmation. Price moving away from a proposed entry does not automatically cancel the setup unless an explicit strategy rule says so.

## Lifecycle states

The proposed state vocabulary includes:

- Detected.

- Awaiting confirmation.

- Confirmed.

- Invalidated.

- Expired.

- Cancelled.

These labels remain proposed until the transition model is approved. Additional internal evaluation or quality states may exist separately.

## Invalidation

A POI is invalidated when a closed candle breaks its defined invalidation boundary.

Setup invalidation follows explicit strategy rules. POI invalidation and setup invalidation must remain distinct concepts.

A setup should not become invalidated merely because its data is stale or a mandatory condition cannot be evaluated.

## Event precedence

When confirmation, expiry and invalidation occur in the same evaluation window, the outcome must follow an explicit, deterministic, versioned precedence policy.

The exact ordering and tie-break rules are still open. They must be specified before the engine can be considered deterministic at lifecycle boundaries.

## Candidate identity and deduplication

Candidate identity is based on:

- Instrument.

- Source POI.

- Direction.

- Strategy version.

However, deduplication must not suppress legitimate new setups indefinitely.

Processing idempotency and business identity are separate concerns. Retries of the same event must not create duplicates, while a valid new setup after a prior terminal state may be permitted under strategy-defined re-entry rules.

## Re-entry

A new setup may be created after a previous setup reaches a terminal state, if the POI and strategy allow it.

The exact terminal states eligible for re-entry, cooldown behavior and identity rules for revised POIs remain open.

## Plan validity

Proposed limit-entry validity is tied to POI expiry, subject to explicit strategy rules.

POI validity, setup lifecycle and entry actionability are distinct concepts.

The exact precedence between expiry, invalidation and confirmation must be defined.
