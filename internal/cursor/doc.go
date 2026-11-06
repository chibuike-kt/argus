// Package cursor persists cmd/argus-worker's per-(instrument,
// timeframe) resume point: the close_time of the last candle fully
// processed through both analysis.Engine and setups.Reconcile.
//
// This is deliberately its own small package rather than a column on
// analysis.StateSnapshot/structure_state. What it tracks is broader
// than analysis-engine state: the worker only advances it once BOTH
// the engine's step AND setups.Reconcile have committed for a candle
// (cmd/argus-worker/processing.go's own per-candle loop), so it
// represents worker-level processing progress across two independent
// subsystems, not a fact belonging to the analysis package's own
// domain. Putting it there would mean that package quietly knowing
// about setups.Reconcile's existence, which it does not and should
// not (setups depends on analysis, never the reverse, same as the
// rest of this codebase's layering).
package cursor
