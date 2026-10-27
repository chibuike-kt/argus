-- GetCurrentSweepByTrigger keys on trigger_open_time, not
-- trigger_candle_id: see 000006's comment on liquidity_sweeps for why
-- a row id is not a safe position key across a candle correction.
-- name: GetCurrentSweepByTrigger :one
SELECT * FROM liquidity_sweeps
WHERE instrument_id = $1 AND timeframe = $2 AND trigger_open_time = $3 AND side = $4
  AND superseded_at IS NULL;

-- name: InsertSweep :one
INSERT INTO liquidity_sweeps (
    instrument_id, timeframe, side, swept_swing_id, trigger_open_time, trigger_candle_id,
    penetration_extreme, reclaim_close, evaluation_time, algorithm_version
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- The two statements below are the supersede handshake's two steps,
-- same pattern and same reason as confirmed_swings' and
-- structure_events'.

-- name: BeginSupersedeSweep :execrows
UPDATE liquidity_sweeps
SET superseded_at = $2
WHERE id = $1 AND superseded_at IS NULL;

-- name: ResolveSupersedeSweep :execrows
UPDATE liquidity_sweeps
SET superseded_by = $2
WHERE id = $1 AND superseded_at IS NOT NULL AND superseded_by IS NULL;
