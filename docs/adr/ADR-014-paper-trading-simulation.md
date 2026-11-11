# ADR-014: Paper-trading simulation

- Status: Confirmed direction; launch timing open
- Category: Optional paper trading
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-006](ADR-006-market-data-contracts-and-canonical-representation.md), [ADR-012](ADR-012-proposed-plans-sizing-and-targets.md)

---

## Decision

Paper trading is an optional downstream module, separate from the core analysis engine.

The user manually activates a simulation. An analytical setup does not automatically become a simulated position.

The initial simulation design includes:

- Multiple isolated paper accounts.

- Versioned simulation assumptions.

- Observable-price entry behavior.

- Bid/ask-aware prices where available and sufficiently fresh.

- No partial fills in the initial simulation version.

- Conservative handling of ambiguous stop/target outcomes.

- Existing simulated positions retain their original terms when a plan is revised.

- Performance reporting that includes modeled costs.

## Fill limitations

An observable price touching a proposed level does not prove that a real order would have filled.

Without historical bid/ask replay, ARGUS cannot claim accurate historical spread-aware fills for periods where the necessary quotes were not retained.

Simulation records must identify the observation time, side, freshness and model version used.

## Ambiguous outcomes

If a candle touches both stop-loss and take-profit and the available data cannot establish which
occurred first, the simulator takes the stop first and flags the outcome as
`ambiguous_resolved_as_stop`. The rule, the fill rule and the other simulation rules are specified in
the strategy spec, section 16, and are shared by the backtest and paper trading. The policy must still
be tested against golden fixtures before the simulation is presented as reliable.

## Open decisions

- Whether paper trading ships in the initial public release.

- Entry-fill rules for the first version are decided (spec section 16); price observation rules beyond
  candles, and bid/ask aware fills, remain open.

- Position adjustment and manual override behavior.

- Cost models and their data requirements.

- Reporting methodology and limitations.
