-- name: GetCurrentSwing :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND pivot_time = $4
  AND superseded_at IS NULL;

-- GetSwingByID looks up a swing directly by id, regardless of whether it
-- is current or superseded -- used to resolve a structure_state
-- reference pointer (which names a specific row, not a position) back
-- to its price.
-- name: GetSwingByID :one
SELECT * FROM confirmed_swings WHERE id = $1;

-- name: InsertSwing :one
INSERT INTO confirmed_swings (
    instrument_id, timeframe, swing_type, pivot_time, price,
    confirmed_at, evaluation_time, algorithm_version, source_candle_ids
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- The two statements below are the supersede handshake's two steps (see
-- 000005's trg_confirmed_swings_enforce_lifecycle comment). Step 1
-- (begin) removes the old row from idx_confirmed_swings_current, making
-- room for a replacement row if one is being inserted; a pure
-- retraction (no replacement) stops after step 1 and never calls step 2.

-- name: BeginSupersedeSwing :execrows
UPDATE confirmed_swings
SET superseded_at = $2
WHERE id = $1 AND superseded_at IS NULL;

-- name: ResolveSupersedeSwing :execrows
UPDATE confirmed_swings
SET superseded_by = $2
WHERE id = $1 AND superseded_at IS NOT NULL AND superseded_by IS NULL;

-- ListLatestCurrentSwings is bounded by max_pivot_time, not merely "the
-- latest current rows overall": during a correction replay, an
-- unbounded query can pick up a swing the walk has not reconciled yet
-- in this pass (a stale row from before the correction that still
-- happens to be "current" only because the replay has not reached its
-- position to supersede or retract it). max_pivot_time must be the
-- latest position this candle's own processing step is entitled to
-- see -- see decideEstablishmentCandidate's call site for the exact
-- bound and why it is <=, not <.
-- name: ListLatestCurrentSwings :many
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time <= $4
ORDER BY pivot_time DESC
LIMIT $5;

-- LatestCurrentSwingBefore bounds by pivot_time, which is only a safe
-- "already reconciled by the walk" bound relative to the POSITION
-- currently being reconciled (see reconcileSwing's retraction
-- fallback) -- it is NOT safe as a bound relative to a resume instant
-- like affectedFrom, because confirmation lags occurrence by
-- RightBars+1 intervals. Use LatestCurrentSwingConfirmedBefore for that.
-- name: LatestCurrentSwingBefore :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time < $4
ORDER BY pivot_time DESC
LIMIT 1;

-- LatestCurrentSwingConfirmedBefore bounds by confirmed_at, not
-- pivot_time: a swing only becomes a usable reference once it is
-- CONFIRMED, and confirmation lags its own pivot_time by
-- (RightBars+1)*interval. Used specifically to reconstruct the as-of
-- state a correction's recompute must resume from -- seeding by
-- occurrence instead of confirmation can hand the resumed walk a
-- reference swing that, at the resume instant, was not actually known
-- yet, and that can emit a wrong BOS or CHOCH on the very first
-- replayed candles.
-- name: LatestCurrentSwingConfirmedBefore :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND confirmed_at <= $4
ORDER BY pivot_time DESC
LIMIT 1;
