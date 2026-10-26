-- RecordCandleGap is idempotent: detecting the same gap twice (a retried
-- or duplicate-delivered detection event) must not create a second row or
-- error. The DO UPDATE is a deliberate no-op (detected_at keeps its
-- original value) purely so RETURNING always yields a row -- ON CONFLICT
-- DO NOTHING would return zero rows on a repeat and break :one.
-- name: RecordCandleGap :one
INSERT INTO candle_gaps (instrument_id, gap_start, gap_end, detected_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (instrument_id, gap_start, gap_end)
DO UPDATE SET detected_at = candle_gaps.detected_at
RETURNING *;

-- ResolveCandleGap only matches a still-open gap (resolved_at IS NULL):
-- reconnecting is not resolution (ADR-006), so this must be called only
-- once backfill or the provider has actually confirmed what happened, and
-- it must not silently re-resolve (or re-describe) an already-resolved
-- gap. Zero rows affected means the store layer needs to look at why
-- (already resolved, or never existed) rather than assume success.
-- name: ResolveCandleGap :one
UPDATE candle_gaps
SET resolved_at = $4, resolution = $5
WHERE instrument_id = $1 AND gap_start = $2 AND gap_end = $3 AND resolved_at IS NULL
RETURNING *;

-- name: ListOpenCandleGaps :many
SELECT * FROM candle_gaps
WHERE instrument_id = $1 AND resolved_at IS NULL
ORDER BY gap_start;

-- GetCandleGap returns a gap regardless of resolved state, so a caller
-- that got zero rows from ResolveCandleGap can tell "never existed" apart
-- from "already resolved" instead of guessing.
-- name: GetCandleGap :one
SELECT * FROM candle_gaps
WHERE instrument_id = $1 AND gap_start = $2 AND gap_end = $3;
