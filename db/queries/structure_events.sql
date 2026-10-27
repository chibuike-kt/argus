-- GetCurrentEventByTrigger keys on trigger_open_time, not
-- trigger_candle_id: see 000007's comment on structure_events for why
-- a row id is not a safe position key across a candle correction.
-- name: GetCurrentEventByTrigger :one
SELECT * FROM structure_events
WHERE instrument_id = $1 AND timeframe = $2 AND trigger_open_time = $3 AND superseded_at IS NULL;

-- name: InsertStructureEvent :one
INSERT INTO structure_events (
    instrument_id, timeframe, event_type, direction_before, direction_after,
    trigger_open_time, trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version,
    sweep_validated, sweep_event_id, protected_swing_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- The two statements below are the supersede handshake's two steps,
-- same pattern and same reason as confirmed_swings'.

-- name: BeginSupersedeEvent :execrows
UPDATE structure_events
SET superseded_at = $2
WHERE id = $1 AND superseded_at IS NULL;

-- name: ResolveSupersedeEvent :execrows
UPDATE structure_events
SET superseded_by = $2
WHERE id = $1 AND superseded_at IS NOT NULL AND superseded_by IS NULL;

-- LatestCurrentEventBefore orders by trigger_open_time because "before
-- a point in time" is meaningful in candle time, not database insertion
-- order. Used, like LatestCurrentSwingBefore, to reconstruct as-of
-- state for a correction's recompute. This was never affected by the
-- trigger_candle_id keying bug (000007) -- it already compared candle
-- open_time via a join, never the row id -- but now that
-- trigger_open_time is a column on this table, the join is unnecessary.
-- name: LatestCurrentEventBefore :one
SELECT * FROM structure_events
WHERE instrument_id = $1 AND timeframe = $2 AND superseded_at IS NULL
  AND trigger_open_time < $3
ORDER BY trigger_open_time DESC
LIMIT 1;
