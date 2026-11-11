// Package outcomes implements docs/strategy/strategy-spec.md section 16:
// what a finalized candle history shows happened to a proposed trade plan.
// It is pure -- no database, no HTTP, no clock reads of its own (every
// time value, including "now", is a parameter) -- so the identical module
// serves both the backtest (internal/backtest) and, later, paper trading,
// exactly as the spec requires: "One outcome module serves both the
// backtest and paper trading, so the two cannot disagree."
package outcomes
