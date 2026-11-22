-- Tracking (Phase 4.1a, D15): which Hyperliquid instruments ARGUS ingests
-- and analyses -- the enabled ones on at least one watchlist, plus the
-- configured floor symbols -- and the audited operation that enables one.

-- ListTrackedInstruments is the tracked set in cap order: the floor first,
-- then by when the instrument was first put on any watchlist, then id. The
-- worker tracks the first cap of them; the API counts them for the cap.
-- Floor symbols are tracked whether or not their row is enabled (the
-- worker always ingested its configured symbols); a watched symbol only
-- when enabled.
-- One index lookup per instrument (idx_watchlist_items_instrument_created)
-- answers both "on any watchlist?" and "since when?" (an EXISTS here ran as
-- a sequential scan per instrument: 70 ms at 50,000 watchlist rows).
-- name: ListTrackedInstruments :many
SELECT i.id, i.native_symbol,
       (i.native_symbol = ANY(sqlc.arg(floor)::text[]))::boolean AS is_floor
FROM instruments i
LEFT JOIN LATERAL (
    SELECT w.created_at FROM watchlist_items w
    WHERE w.instrument_id = i.id
    ORDER BY w.created_at ASC
    LIMIT 1
) first_watch ON true
WHERE i.provider = 'hyperliquid'
  AND (i.native_symbol = ANY(sqlc.arg(floor)::text[])
       OR (i.enabled AND first_watch.created_at IS NOT NULL))
ORDER BY (i.native_symbol = ANY(sqlc.arg(floor)::text[])) DESC,
         first_watch.created_at ASC NULLS FIRST,
         i.id ASC;

-- LockTrackedCap serialises every add that could grow the tracked set, for
-- the rest of the transaction, so two concurrent adds of two different new
-- symbols can never both pass the cap check.
-- name: LockTrackedCap :exec
SELECT pg_advisory_xact_lock(hashtext('argus:tracked-symbols-cap'));

-- name: GetInstrumentForUpdate :one
SELECT * FROM instruments WHERE id = $1 FOR UPDATE;

-- EnableInstrumentForWatchlist is the one code path that sets enabled =
-- TRUE (D15.1): only from a watchlist add, only outside production (the
-- caller's guard), always with an instrument_enablements row in the same
-- transaction. Zero rows: it was already enabled.
-- name: EnableInstrumentForWatchlist :execrows
UPDATE instruments SET enabled = TRUE, updated_at = now() WHERE id = $1 AND enabled = FALSE;

-- name: InsertInstrumentEnablement :one
INSERT INTO instrument_enablements (instrument_id, user_id, reason, environment)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListInstrumentEnablements :many
SELECT * FROM instrument_enablements WHERE instrument_id = $1 ORDER BY id;

-- name: NextWatchlistPosition :one
SELECT (coalesce(max(position), -1) + 1)::integer FROM watchlist_items WHERE user_id = $1;

-- name: GetWatchlistItem :one
SELECT * FROM watchlist_items WHERE user_id = $1 AND instrument_id = $2;

-- ListWatchlistItemsForUpdate locks the user's rows for a reorder, so a
-- concurrent add or remove waits and the permutation check is exact.
-- name: ListWatchlistItemsForUpdate :many
SELECT * FROM watchlist_items WHERE user_id = $1 ORDER BY position ASC FOR UPDATE;

-- name: SetWatchlistItemPosition :exec
UPDATE watchlist_items SET position = $3 WHERE user_id = $1 AND instrument_id = $2;

-- UpsertTrackedSymbolState never replaces needs_attention with anything
-- else (D15.5): a running pipeline's own "live" or "retrying" must not
-- hide a precision change. Clearing it is the operator's decision.
-- name: UpsertTrackedSymbolState :exec
INSERT INTO tracked_symbol_states (instrument_id, state, reason, attempt, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (instrument_id) DO UPDATE SET
    state = EXCLUDED.state, reason = EXCLUDED.reason, attempt = EXCLUDED.attempt, updated_at = EXCLUDED.updated_at
WHERE tracked_symbol_states.state <> 'needs_attention' OR EXCLUDED.state = 'needs_attention';

-- name: ListTrackedSymbolStates :many
SELECT * FROM tracked_symbol_states WHERE instrument_id = ANY(sqlc.arg(instrument_ids)::bigint[]);

-- name: ListHyperliquidInstruments :many
SELECT * FROM instruments WHERE provider = 'hyperliquid' ORDER BY id;
