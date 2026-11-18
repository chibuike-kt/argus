-- name: GetCurrentCandle :one
SELECT * FROM candles
WHERE instrument_id = $1 AND open_time = $2 AND superseded_at IS NULL;

-- GetLatestClosedCandle returns the most recent current, closed candle
-- for an instrument -- used to bound a replay (e.g. a candle
-- correction's recompute) at "now" in candle terms, without the caller
-- needing to know what "now" means for this instrument's series.
-- name: GetLatestClosedCandle :one
SELECT * FROM candles
WHERE instrument_id = $1 AND superseded_at IS NULL AND is_closed = true
ORDER BY open_time DESC
LIMIT 1;

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

-- ListClosedCandlesInRangeLimited is GET /instruments/{id}/candles' own
-- read path (internal/httpapi): current, CLOSED candles only (a
-- forming candle's own values can still change, so it is never a
-- reliable thing to hand a client), oldest first, with the hard LIMIT
-- applied in the query itself -- not fetched in full and truncated in
-- Go -- so a wide [from, to) cannot make Postgres materialize more
-- rows than the endpoint will ever return, which is the entire point
-- of the cap (worker-hardening brief, Part C).
-- name: ListClosedCandlesInRangeLimited :many
SELECT * FROM candles
WHERE instrument_id = $1 AND superseded_at IS NULL AND is_closed = true
  AND open_time >= $2 AND open_time < $3
ORDER BY open_time
LIMIT $4;

-- ListClosedCandlesInRangeLimitedDesc is ListClosedCandlesInRangeLimited's
-- own sibling for GET /instruments/{id}/candles?order=desc (web-wiring-
-- followup brief, item 1: "give me the most recent N" needs no range at
-- all, not just a wide one). sqlc needs the ORDER BY direction literal in
-- the SQL text itself -- it cannot be a bound parameter -- so this is a
-- second, otherwise-identical query rather than one query with a dynamic
-- order. from_time/to_time are nullable (sqlc.narg): NULL means "no lower/
-- upper bound", which is the whole point here -- a caller asking for the
-- latest N candles should not have to invent a [from, to) range just to
-- satisfy a required parameter. limit is still enforced in the query
-- itself regardless of how open the bounds are.
-- name: ListClosedCandlesInRangeLimitedDesc :many
SELECT * FROM candles
WHERE instrument_id = $1 AND superseded_at IS NULL AND is_closed = true
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR open_time >= sqlc.narg('from_time')::timestamptz)
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR open_time < sqlc.narg('to_time')::timestamptz)
ORDER BY open_time DESC
LIMIT $2;

-- UpsertFormingCandle inserts the first tick of a new minute, or overwrites
-- a still-forming row in place for a later tick of the same minute.
-- idx_candles_current (UNIQUE on (instrument_id, open_time) WHERE
-- superseded_at IS NULL) is exactly the conflict target.
--
-- An already-CLOSED current row is never touched: the DO UPDATE only
-- applies WHERE the row is still forming, so a closed row yields zero rows
-- (the store reports ErrCandleAlreadyClosed) instead of the UPDATE reaching
-- trg_candles_enforce_lifecycle's "closed: OHLCV and is_closed are frozen"
-- rejection. That case is a real race, not a caller bug: a REST backfill
-- reads a minute as still forming, the live stream closes it, then the
-- backfill writes its (older) forming value -- seen on the dev stack
-- 2026-11-17T00:34:59Z. A forming value must never overwrite a close.
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
WHERE candles.is_closed = false
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

-- CloseCandleWithValues freezes a forming candle WITH the final values in
-- the same statement. Closing with whatever the row happens to hold (as
-- CloseCandle does) lets a concurrent writer's stale forming value -- a
-- REST backfill fetched while the minute was still forming -- land between
-- this writer's upsert and its close and be frozen as the closed candle.
-- Zero rows: the minute is already closed, or does not exist.
-- name: CloseCandleWithValues :one
UPDATE candles
SET open = $3, high = $4, low = $5, close = $6, volume = $7,
    tick_aligned = $8, provider = $9, ingested_at = $10, is_closed = true
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

-- GetCandleStatsInputs is GET /instruments/stats' own read path (real-
-- panel-data brief): everything the side panel's 24h change, 24h volume
-- and performance tiles need for one instrument, in one statement.
--
--   * latest: the latest current, closed 1-minute candle (the same row
--     GetLatestClosedCandle returns).
--   * vol: count and sum of base volume over the current, closed
--     candles with open_time in (latest - volume_window_seconds,
--     latest] -- the caller treats the sum as present only when count
--     equals the full window (no gap, nothing guessed).
--   * reference_found/reference_closes: for each offset in
--     offsets_seconds (in order), whether a current, closed candle
--     exists at EXACTLY latest.open_time - offset, and its close (0
--     when not found -- reference_found is the only signal; a nearby
--     candle is never used, nothing is interpolated).
--
-- Offsets are whole seconds multiplied by interval '1 second', never
-- '1 day' / '1 month' intervals: timestamptz minus a day or month
-- interval depends on the session TimeZone (a DST change makes a
-- "day" 23 or 25 hours), and this result must not.
--
-- Cost: every lookup rides idx_candles_current (instrument_id,
-- open_time) WHERE superseded_at IS NULL -- one backward index probe for
-- latest, one probe per offset, and one index range scan of at most
-- volume_window_seconds/60 rows for vol: O(log n + window), independent
-- of how much history the instrument has. No row means the instrument
-- has no closed candle at all.
-- name: GetCandleStatsInputs :one
WITH latest AS (
    SELECT c.open_time, c.close
    FROM candles c
    WHERE c.instrument_id = sqlc.arg(instrument_id)
      AND c.superseded_at IS NULL AND c.is_closed = true
    ORDER BY c.open_time DESC
    LIMIT 1
)
SELECT
    latest.open_time AS latest_open_time,
    latest.close AS latest_close,
    vol.n AS volume_window_count,
    vol.total AS volume_window_sum,
    ARRAY(
        SELECT r.id IS NOT NULL
        FROM unnest(sqlc.arg(offsets_seconds)::bigint[]) WITH ORDINALITY AS o(secs, ord)
        LEFT JOIN candles r
          ON r.instrument_id = sqlc.arg(instrument_id)
         AND r.superseded_at IS NULL AND r.is_closed = true
         AND r.open_time = latest.open_time - o.secs * interval '1 second'
        ORDER BY o.ord
    )::boolean[] AS reference_found,
    ARRAY(
        SELECT coalesce(r.close, 0)
        FROM unnest(sqlc.arg(offsets_seconds)::bigint[]) WITH ORDINALITY AS o(secs, ord)
        LEFT JOIN candles r
          ON r.instrument_id = sqlc.arg(instrument_id)
         AND r.superseded_at IS NULL AND r.is_closed = true
         AND r.open_time = latest.open_time - o.secs * interval '1 second'
        ORDER BY o.ord
    )::bigint[] AS reference_closes
FROM latest
CROSS JOIN LATERAL (
    SELECT count(*)::bigint AS n, coalesce(sum(v.volume), 0)::bigint AS total
    FROM candles v
    WHERE v.instrument_id = sqlc.arg(instrument_id)
      AND v.superseded_at IS NULL AND v.is_closed = true
      AND v.open_time > latest.open_time - sqlc.arg(volume_window_seconds)::bigint * interval '1 second'
      AND v.open_time <= latest.open_time
) vol;

-- ListMissingCandleRanges is the integrity scan's one statement: every
-- minute in [from_time, to_time) with no current, closed 1-minute candle,
-- grouped into consecutive ranges [gap_start, gap_end), starting at the
-- first stored closed candle at or after from_time. Minutes older than
-- lost_before (the exchange's retention edge) that an OPEN candle_gaps row
-- already covers are skipped: known, recorded holes that can never be
-- fetched, so they are not reported or recorded again on every scan.
-- Minutes inside retention are always returned, whatever candle_gaps says:
-- an open gap row there is no proof the minutes cannot be fetched (the
-- dev stack has hundreds of unaligned, unresolved rows recorded by the
-- periodic backfill's own tail), and a fetchable hole must be filled. The series steps a fixed 1 minute, so the session TimeZone
-- (and DST) cannot change which minutes are expected.
--
-- Cost: one index probe on idx_candles_current per expected minute (the
-- anti-join) plus a probe of candle_gaps -- O(window minutes), independent
-- of total history; the grouping is one window function over the missing
-- minutes only.
-- name: ListMissingCandleRanges :many
WITH missing AS (
    SELECT m AS open_time
    FROM generate_series(
        -- Start at the first stored candle in the window, not before it:
        -- minutes before an instrument's history begins are not holes.
        greatest(sqlc.arg(from_time)::timestamptz, (
            SELECT min(c0.open_time) FROM candles c0
            WHERE c0.instrument_id = sqlc.arg(instrument_id) AND c0.superseded_at IS NULL
              AND c0.is_closed = true AND c0.open_time >= sqlc.arg(from_time)::timestamptz)),
        sqlc.arg(to_time)::timestamptz - interval '1 minute', interval '1 minute') AS m
    WHERE NOT EXISTS (
        SELECT 1 FROM candles c
        WHERE c.instrument_id = sqlc.arg(instrument_id) AND c.open_time = m
          AND c.superseded_at IS NULL AND c.is_closed = true
    )
    AND NOT (m < sqlc.arg(lost_before)::timestamptz AND EXISTS (
        SELECT 1 FROM candle_gaps g
        WHERE g.instrument_id = sqlc.arg(instrument_id) AND g.resolved_at IS NULL
          AND m >= g.gap_start AND m < g.gap_end
    ))
), grouped AS (
    SELECT open_time, open_time - (row_number() OVER (ORDER BY open_time)) * interval '1 minute' AS grp
    FROM missing
)
SELECT min(open_time)::timestamptz AS gap_start,
       (max(open_time) + interval '1 minute')::timestamptz AS gap_end,
       count(*)::bigint AS minutes
FROM grouped
GROUP BY grp
ORDER BY gap_start;

-- CountClosedCandlesInRange counts current, closed 1-minute candles with
-- open_time in [from_time, to_time) -- the integrity scan's "how many of
-- this hole's minutes are now filled" check.
-- name: CountClosedCandlesInRange :one
SELECT count(*)::bigint FROM candles
WHERE instrument_id = sqlc.arg(instrument_id) AND superseded_at IS NULL AND is_closed = true
  AND open_time >= sqlc.arg(from_time)::timestamptz AND open_time < sqlc.arg(to_time)::timestamptz;
