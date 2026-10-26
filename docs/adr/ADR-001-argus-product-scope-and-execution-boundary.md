# ADR-001: ARGUS product scope and execution boundary

- Status: Confirmed
- Category: Product definition and boundaries
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-012](ADR-012-proposed-plans-sizing-and-targets.md), [ADR-014](ADR-014-paper-trading-simulation.md)

---

## Context

ARGUS is an autonomous technical-analysis tool for crypto perpetual futures and forex. It should behave like a skilled technical analyst conducting multi-timeframe analysis on a live chart, rather than a system that simply computes indicators and emits buy or sell alerts.

The user must be able to understand how ARGUS reached a conclusion, inspect its chart annotations, track a developing setup and decide whether to execute the trade independently.

## Decision

ARGUS will:

- Monitor a curated watchlist of supported crypto perpetual futures and major forex pairs.

- Determine higher-timeframe market context and structure.

- Identify supply and demand zones, support and resistance, and other points of interest.

- Evaluate lower-timeframe entry conditions and confluences.

- Produce proposed entry, stop-loss, take-profit targets, invalidation, risk estimates and supporting reasoning.

- Track candidates and setups through their lifecycle.

- Reevaluate affected setups when relevant market data or strategy conditions change.

- Display analysis on interactive live charts.

- Notify users about meaningful setup transitions.

- Allow users to inspect the evidence behind each analytical conclusion.

ARGUS will not place real trades in its initial architecture. Users will execute trades manually through their own exchange application or MetaTrader 5 (MT5), as applicable.

A proposed limit entry is an analytical plan. It is not an order submitted to a broker or exchange.

## Consequences

- No real-execution engine, broker order-placement workflow or exchange execution credentials are required for the initial release.

- The core domain model must represent proposed trading plans, not actual broker orders.

- Any paper-trading capability remains separate from the analytical engine.

- LONG and SHORT chart annotations must not imply that an actual order was submitted.
