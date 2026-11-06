-- 000009_processing_cursors: cmd/argus-worker's persisted resume point
-- per (instrument, timeframe) -- worker-hardening brief, Part B.
--
-- processor.cursor (cmd/argus-worker/processing.go) was in-memory: safe
-- (analysis.Engine and setups.Reconcile are both idempotent over an
-- already-processed range) but wasteful on every restart, replaying
-- an instrument's entire stored candle history. This table makes that
-- resume point durable.
--
-- A plain mutable projection, the same role structure_state plays for
-- direction (000005's own comment) -- written in place via upsert,
-- never through a supersede handshake, because this is a cursor, not
-- evidence: there is nothing to reconstruct from an event log, and no
-- history of past cursor values is ever needed.
--
-- Lives in its own table, not a column bolted onto structure_state,
-- because what it tracks is broader than the analysis engine alone:
-- the worker only advances it after BOTH analysis.Engine's step and
-- setups.Reconcile have committed for a candle (processing.go's own
-- per-candle loop), so it represents worker-level processing progress,
-- not analysis-package state (hence internal/cursor, a new small
-- package, rather than living in internal/analysis -- see that
-- package's own doc comment for the reasoning in full).
CREATE TABLE processing_cursors (
    instrument_id              BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe                  TEXT        NOT NULL,
    last_processed_close_time  TIMESTAMPTZ NOT NULL,
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (instrument_id, timeframe)
);
