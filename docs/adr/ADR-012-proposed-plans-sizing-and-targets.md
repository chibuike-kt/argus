# ADR-012: Proposed plans, sizing and targets

- Status: Partially confirmed
- Category: Entry, risk and trading plans
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md), [ADR-010](ADR-010-setup-state-invalidation-and-re-entry.md), [ADR-014](ADR-014-paper-trading-simulation.md)

---

## Decision

The analysis engine will construct a proposed trading plan without submitting an order.

Entry behavior is strategy-defined and versioned.

The plan must make clear whether it is awaiting confirmation, actionable under the current strategy, expired or invalidated.

## Stop-loss

The stop-loss is based on the relevant invalidation level with a configurable volatility-based buffer.

The exact volatility source, multiplier, rounding policy and instrument-specific constraints remain open.

## Take-profit targets

Targets are derived from eligible structural levels.

Multiple targets are supported. Targets must be directionally ordered and distinct.

The exact target-selection algorithm, treatment of overlapping structures and minimum-distance requirements remain open.

## Risk and position sizing

Users configure their own account value and risk preferences.

Indicative sizing is instrument-aware and based on the relationship between entry, stop distance, configured risk and the instrument's verified contract conventions.

Leverage and margin are not substitutes for stop-loss risk. ARGUS must display these concepts separately if relevant data is available.

Contract size, tick value, pip conventions, quote-currency conversion and rounding rules must be validated for each supported instrument.

## Risk-to-reward

Risk-to-reward is displayed as information, not used as an automatic eligibility filter.

Each target may have its own risk-to-reward calculation.

## Trading costs

ARGUS estimates fees, spread and other costs only when the relevant source and assumptions are known.

Unknown costs must not be represented as precise facts.

Historical cost and fill estimates must disclose limitations caused by unavailable historical bid/ask data.

## User modifications

Users may add annotations to analytical plans.

User modifications do not silently change the underlying strategy output. A revised analytical plan requires an explicit new version under the relevant policy.
