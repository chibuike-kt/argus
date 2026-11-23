# ADR-012: Proposed plans, sizing and targets

- Status: Partially confirmed
- Category: Entry, risk and trading plans
- Version: 0.1.0-draft
- Last updated: 22 November 2026
- Amended: 22 November 2026 (D12, D16)
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

Amended 22 November 2026 (decision D16, strategy version swing-structure-v2): a plan carries exactly
one target, the nearest eligible structural level whose risk-to-reward is at least the strategy's
minimum. The statement above that multiple targets are supported applies to strategy version
swing-structure-v1 and earlier, whose stored plans keep their targets unchanged.

## Risk and position sizing

Users configure their own account value and risk preferences.

Indicative sizing is instrument-aware and based on the relationship between entry, stop distance, configured risk and the instrument's verified contract conventions.

Leverage and margin are not substitutes for stop-loss risk. ARGUS must display these concepts separately if relevant data is available.

Contract size, tick value, pip conventions, quote-currency conversion and rounding rules must be validated for each supported instrument.

## Risk-to-reward

Each candidate target has its own risk-to-reward calculation. The plan displays the chosen target's.

Amended 22 November 2026 by owner decisions D12 and D16: a plan carries one target, the nearest
liquidity level whose risk-to-reward is at least the strategy's minimum (2.0 in strategy version
swing-structure-v2), and a plan with no such level is not viable. A
plan that is not viable is recorded as a rejected setup with the reason and the evidence, and never
becomes actionable. The minimum is a versioned strategy parameter. This is a viability rule on the
analysis, not an order or an execution decision (ADR-001).

## Trading costs

ARGUS estimates fees, spread and other costs only when the relevant source and assumptions are known.

Unknown costs must not be represented as precise facts.

Historical cost and fill estimates must disclose limitations caused by unavailable historical bid/ask data.

## User modifications

Users may add annotations to analytical plans.

User modifications do not silently change the underlying strategy output. A revised analytical plan requires an explicit new version under the relevant policy.
