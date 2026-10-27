// Package analysis is a feature module boundary (see ADR-003):
// deterministic, versioned swing and market-structure detection, per
// docs/strategy/strategy-spec.md v0.1 (ADR-009, ADR-011, ADR-019).
//
// Scope of this version (spec "Scope of this version"): single
// instrument, single timeframe swing detection, structural direction
// state, and BOS/CHOCH classification. Higher-timeframe context,
// multi-timeframe alignment, POIs, confluences, setups, and everything
// else ADR-009 eventually wants are explicitly out of scope and not
// guessed at here.
//
// "Single timeframe" without the spec naming which one: this package
// assumes the canonical 1-minute persisted series (ADR-006), the only
// series actually stored. 000005's confirmed_swings and structure_events
// tables carry an explicit timeframe column and their Go counterparts
// carry an explicit timeframe parameter, but their foreign keys
// (trigger_candle_id, source_candle_ids) reference candles(id)
// specifically -- the 1-minute table, nothing else. Evaluating structure
// on a derived timeframe (15m, 1h, ...) is a later migration (a new
// evidence table, or a materialized aggregate-candle table for these FKs
// to point at instead), not something a config value can turn on today.
//
// Engine is the one implementation live incremental evaluation, batch
// replay, and correction recompute all share (ADR-009): the distinction
// between those three is entirely which range of candles the caller asks
// Engine.EvaluateRange to process, never a branch inside the algorithm
// itself. See engine.go for the per-candle atomic step and
// HandleCandleCorrection for how a candle correction's recompute
// reconstructs the as-of state it must resume from, rather than trusting
// the live (and about-to-be-partially-superseded) structure_state
// projection.
package analysis
