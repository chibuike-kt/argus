-- name: GetCurrentEventByTrigger :one
SELECT * FROM structure_events
WHERE instrument_id = $1 AND timeframe = $2 AND trigger_candle_id = $3 AND superseded_at IS NULL;

-- name: InsertStructureEvent :one
INSERT INTO structure_events (
    instrument_id, timeframe, event_type, direction_before, direction_after,
    trigger_candle_id, reference_swing_ids, evaluation_time, algorithm_version,
    sweep_validated, sweep_event_id, protected_swing_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
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

-- LatestCurrentEventBefore joins through to the trigger candle's
-- open_time because ordering "before a point in time" is meaningful in
-- candle time, not database insertion order. Used, like
-- LatestCurrentSwingBefore, to reconstruct as-of state for a
-- correction's recompute.
-- name: LatestCurrentEventBefore :one
SELECT se.* FROM structure_events se
JOIN candles c ON c.id = se.trigger_candle_id
WHERE se.instrument_id = $1 AND se.timeframe = $2 AND se.superseded_at IS NULL
  AND c.open_time < $3
ORDER BY c.open_time DESC
LIMIT 1;

-- LatestInvalidatingChochAfter finds whether a CHOCH flipping away
-- from direction_before has occurred strictly after a protected
-- anchor's establishment and strictly before the correction's resume
-- point (spec section 7: "invalidated... that close is the CHOCH that
-- flips direction"). Existence alone is enough -- reconstructStateAsOf
-- only needs to know whether the candidate anchor survived to asOf,
-- not which exact CHOCH ended it.
-- name: LatestInvalidatingChochAfter :one
SELECT se.* FROM structure_events se
JOIN candles c ON c.id = se.trigger_candle_id
WHERE se.instrument_id = $1 AND se.timeframe = $2 AND se.superseded_at IS NULL
  AND se.event_type = 'choch' AND se.direction_before = $3
  AND c.open_time > $4 AND c.open_time < $5
ORDER BY c.open_time DESC
LIMIT 1;
