-- Read-only views for the chart layers, the engine status line and the
-- activity feed (product overhaul brief, Phase 6.2 and 6.5, D7). Every row
-- comes from stored structure, setup events, cursors and gaps; nothing is
-- derived here that the engine did not record.

-- ListSwingsInWindow: current confirmed swings whose pivot candle opened in
-- [from_time, to_time), oldest first, at most row_limit rows.
-- name: ListSwingsInWindow :many
SELECT id, swing_type, pivot_time, price, confirmed_at
FROM confirmed_swings
WHERE instrument_id = sqlc.arg(instrument_id) AND timeframe = sqlc.arg(timeframe)
  AND superseded_at IS NULL
  AND pivot_time >= sqlc.arg(from_time)::timestamptz AND pivot_time < sqlc.arg(to_time)::timestamptz
ORDER BY pivot_time, id
LIMIT sqlc.arg(row_limit);

-- ListStructureEventsInWindow: current BOS, CHOCH and direction-established
-- events whose trigger candle opened in [from_time, to_time), oldest first.
-- A BOS or CHOCH names exactly one broken swing (000005); its price and
-- pivot time come with it so a client can draw the line from the broken
-- swing to the break candle. For direction_established the broken-swing
-- columns are NULL (it names four swings, none of them "broken").
-- name: ListStructureEventsInWindow :many
SELECT
    se.id,
    se.event_type,
    se.direction_before,
    se.direction_after,
    se.trigger_open_time,
    se.sweep_validated,
    se.sweep_event_id,
    se.protected_swing_id,
    sw.id AS broken_swing_id,
    sw.price AS broken_swing_price,
    sw.pivot_time AS broken_swing_time
FROM structure_events se
LEFT JOIN confirmed_swings sw
    ON se.event_type IN ('bos', 'choch') AND sw.id = se.reference_swing_ids[1]
WHERE se.instrument_id = sqlc.arg(instrument_id) AND se.timeframe = sqlc.arg(timeframe)
  AND se.superseded_at IS NULL
  AND se.trigger_open_time >= sqlc.arg(from_time)::timestamptz AND se.trigger_open_time < sqlc.arg(to_time)::timestamptz
ORDER BY se.trigger_open_time, se.id
LIMIT sqlc.arg(row_limit);

-- ListSweepsInWindow: current liquidity sweeps whose trigger candle opened
-- in [from_time, to_time), oldest first, with the swept swing's price and
-- pivot time.
-- name: ListSweepsInWindow :many
SELECT
    ls.id,
    ls.side,
    ls.trigger_open_time,
    ls.penetration_extreme,
    ls.reclaim_close,
    sw.id AS swept_swing_id,
    sw.price AS swept_swing_price,
    sw.pivot_time AS swept_swing_time
FROM liquidity_sweeps ls
JOIN confirmed_swings sw ON sw.id = ls.swept_swing_id
WHERE ls.instrument_id = sqlc.arg(instrument_id) AND ls.timeframe = sqlc.arg(timeframe)
  AND ls.superseded_at IS NULL
  AND ls.trigger_open_time >= sqlc.arg(from_time)::timestamptz AND ls.trigger_open_time < sqlc.arg(to_time)::timestamptz
ORDER BY ls.trigger_open_time, ls.id
LIMIT sqlc.arg(row_limit);

-- ListEngineStatus: every enabled instrument with its processing cursor
-- (absent when the worker has never evaluated it), and the newest stored
-- closed 1-minute candle and how many closed candles close after the
-- cursor -- how far the analysis is behind the data that is stored.
-- has_cursor false: the cursor columns hold the epoch and must not be read;
-- has_closed_candle false likewise for latest_closed_close_time.
-- name: ListEngineStatus :many
SELECT
    i.id AS instrument_id,
    i.native_symbol,
    i.provider,
    (pc.instrument_id IS NOT NULL)::boolean AS has_cursor,
    coalesce(pc.timeframe, '')::text AS timeframe,
    coalesce(pc.last_processed_close_time, 'epoch'::timestamptz)::timestamptz AS evaluated_through,
    coalesce(pc.updated_at, 'epoch'::timestamptz)::timestamptz AS last_evaluated_at,
    (lc.close_time IS NOT NULL)::boolean AS has_closed_candle,
    coalesce(lc.close_time, 'epoch'::timestamptz)::timestamptz AS latest_closed_close_time,
    (
        SELECT count(*)
        FROM candles c
        WHERE c.instrument_id = i.id AND c.interval = sqlc.arg(interval) AND c.superseded_at IS NULL AND c.is_closed = true
          AND c.open_time >= coalesce(pc.last_processed_close_time, 'epoch'::timestamptz)
    )::bigint AS closed_candles_after_cursor
FROM instruments i
LEFT JOIN processing_cursors pc ON pc.instrument_id = i.id AND pc.timeframe = '1m'
LEFT JOIN LATERAL (
    SELECT c.close_time
    FROM candles c
    WHERE c.instrument_id = i.id AND c.interval = sqlc.arg(interval) AND c.superseded_at IS NULL AND c.is_closed = true
    ORDER BY c.open_time DESC
    LIMIT 1
) lc ON true
WHERE i.enabled = true
ORDER BY i.id;

-- ListActivity: the activity feed, newest first, from stored rows only (D7):
-- setup events (created or changed state), structure events (BOS, CHOCH,
-- direction established), and ingestion gaps that were resolved (recovered
-- or confirmed to have no data). Open gaps are not activity: nothing has
-- happened to them yet.
--
-- at is when ARGUS recorded the row (created_at, or resolved_at for a gap);
-- event_time is the candle time it is about. Keyset pagination on
-- (at, kind, id) strictly below the cursor; the first page passes a cursor
-- in the far future. Events created in one transaction share created_at,
-- which is why kind and id are part of the key.
-- name: ListActivity :many
SELECT kind, id, instrument_id, timeframe, at, event_time, detail, setup_id, direction, poi_type, found_mode, gap_end
FROM (
    SELECT
        'setup'::text AS kind,
        e.id,
        s.instrument_id,
        s.timeframe,
        e.created_at AS at,
        e.evaluation_time AS event_time,
        e.event_type AS detail,
        s.id AS setup_id,
        s.direction,
        s.poi_type,
        s.found_mode,
        NULL::timestamptz AS gap_end
    FROM setup_events e
    JOIN setups s ON s.id = e.setup_id
    WHERE sqlc.arg(include_setups)::boolean
      AND s.instrument_id = ANY(sqlc.arg(instrument_ids)::bigint[])
      AND (e.created_at, 'setup'::text, e.id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_kind)::text, sqlc.arg(before_id)::bigint)
    UNION ALL
    SELECT
        'structure'::text,
        se.id,
        se.instrument_id,
        se.timeframe,
        se.created_at,
        se.trigger_open_time,
        se.event_type,
        0::bigint,
        se.direction_after,
        ''::text,
        ''::text,
        NULL::timestamptz
    FROM structure_events se
    WHERE sqlc.arg(include_structure)::boolean
      AND se.superseded_at IS NULL
      AND se.instrument_id = ANY(sqlc.arg(instrument_ids)::bigint[])
      AND (se.created_at, 'structure'::text, se.id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_kind)::text, sqlc.arg(before_id)::bigint)
    UNION ALL
    SELECT
        'gap'::text,
        g.id,
        g.instrument_id,
        '1m'::text,
        g.resolved_at,
        g.gap_start,
        coalesce(g.resolution, '')::text,
        0::bigint,
        ''::text,
        ''::text,
        ''::text,
        g.gap_end
    FROM candle_gaps g
    WHERE sqlc.arg(include_gaps)::boolean
      AND g.resolved_at IS NOT NULL
      AND g.instrument_id = ANY(sqlc.arg(instrument_ids)::bigint[])
      AND (g.resolved_at, 'gap'::text, g.id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_kind)::text, sqlc.arg(before_id)::bigint)
) a
ORDER BY at DESC, kind DESC, id DESC
LIMIT sqlc.arg(row_limit);
