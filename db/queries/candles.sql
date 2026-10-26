-- name: GetCurrentCandle :one
SELECT * FROM candles
WHERE instrument_id = $1 AND open_time = $2 AND superseded_at IS NULL;

-- ListCurrentCandlesInRange is the read path aggregation uses: the current
-- (non-superseded) row for every open_time in [start, end), ordered so the
-- caller can scan once for first/last open/close and reduce for high/low/
-- sum. A missing open_time in the result set is exactly the gap the caller
-- is responsible for detecting; this query does not fill gaps.
-- name: ListCurrentCandlesInRange :many
SELECT * FROM candles
WHERE instrument_id = $1 AND superseded_at IS NULL
  AND open_time >= $2 AND open_time < $3
ORDER BY open_time;

-- UpsertFormingCandle inserts the first tick of a new minute, or overwrites
-- a still-forming row in place for a later tick of the same minute.
-- idx_candles_current (UNIQUE on (instrument_id, open_time) WHERE
-- superseded_at IS NULL) is exactly the conflict target. If the existing
-- current row is already closed, trg_candles_enforce_lifecycle rejects the
-- UPDATE outright -- a tick arriving for an already-closed minute is a
-- caller bug, not a case this query papers over.
-- name: UpsertFormingCandle :one
INSERT INTO candles (
    instrument_id, open_time, close_time, open, high, low, close, volume,
    revision, is_closed, tick_aligned, provider, ingested_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, FALSE, $9, $10, $11)
ON CONFLICT (instrument_id, open_time) WHERE superseded_at IS NULL
DO UPDATE SET
    open = EXCLUDED.open,
    high = EXCLUDED.high,
    low = EXCLUDED.low,
    close = EXCLUDED.close,
    volume = EXCLUDED.volume,
    tick_aligned = EXCLUDED.tick_aligned,
    provider = EXCLUDED.provider,
    ingested_at = EXCLUDED.ingested_at
RETURNING *;

-- CloseCandle freezes a forming candle. The is_closed = false guard makes a
-- repeat close of an already-closed candle affect zero rows instead of
-- hitting trg_candles_enforce_lifecycle's handshake-step rejection, so the
-- store layer can treat "already closed" as an idempotent outcome rather
-- than a confusing trigger error (guide 14: queue/worker retries must not
-- turn a repeat delivery into a spurious failure).
-- name: CloseCandle :one
UPDATE candles
SET is_closed = true
WHERE instrument_id = $1 AND open_time = $2 AND superseded_at IS NULL AND is_closed = false
RETURNING *;

-- The three statements below are the correction handshake's three steps
-- (see 000003's trg_candles_enforce_lifecycle comment and
-- marketdata.Store.SupersedeCandle, which runs all three in one
-- transaction). None of them is meaningful on its own.

-- name: BeginSupersedeCandle :execrows
UPDATE candles
SET superseded_at = $2
WHERE id = $1 AND is_closed = true AND superseded_at IS NULL;

-- name: InsertCorrectingCandle :one
INSERT INTO candles (
    instrument_id, open_time, close_time, open, high, low, close, volume,
    revision, is_closed, tick_aligned, provider, ingested_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE, $10, $11, $12)
RETURNING *;

-- name: ResolveSupersedeCandle :execrows
UPDATE candles
SET superseded_by = $2
WHERE id = $1 AND superseded_at IS NOT NULL AND superseded_by IS NULL;
