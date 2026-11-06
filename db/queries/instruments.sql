-- name: GetInstrumentByID :one
SELECT * FROM instruments WHERE id = $1;

-- name: GetInstrumentByProviderAndSymbol :one
SELECT * FROM instruments WHERE provider = $1 AND native_symbol = $2;

-- name: ListEnabledInstruments :many
SELECT * FROM instruments WHERE enabled = TRUE ORDER BY provider, native_symbol;

-- ListAllInstruments is GET /instruments' own read path -- every
-- registered instrument regardless of enablement, unlike
-- ListEnabledInstruments (the worker's own read path). Adding neither
-- a WHERE clause nor any way to filter by enabled here: nothing in this
-- query file sets enabled = TRUE either, so returning the full list is
-- not a wider exposure of anything ListEnabledInstruments did not
-- already expose the shape of.
-- name: ListAllInstruments :many
SELECT * FROM instruments ORDER BY provider, native_symbol;

-- ListAllInstrumentsPaged is GET /instruments' own read path as of the
-- backend-guards-cleanup brief (project-status audit, section D: the
-- one list endpoint with no cap). Keyset pagination on id, not on
-- (provider, native_symbol) the way ListAllInstruments above orders --
-- id is a stable, monotonic BIGSERIAL well-suited to "id > after_id",
-- which (provider, native_symbol) is not without a composite cursor no
-- other endpoint in this API needs either. after_id is nullable
-- (sqlc.narg): NULL means "from the start," matching a first page with
-- no cursor yet. This is a new, separate ordering from
-- ListAllInstruments's own -- that query is untouched, still available,
-- still unbounded, in case anything else in this package ever needs
-- the old order.
-- name: ListAllInstrumentsPaged :many
SELECT * FROM instruments
WHERE sqlc.narg('after_id')::bigint IS NULL OR id > sqlc.narg('after_id')::bigint
ORDER BY id
LIMIT $1;

-- InsertInstrument always writes enabled = FALSE. There is no parameter for
-- it: enablement cannot be set through this query, and no other query in
-- this file sets it either (ADR-006, ADR-007 -- enablement stays gated on
-- data rights that are not yet confirmed).
--
-- price_scale, qty_scale, tick_size and qty_step_size have no column
-- default (000002): every value must come from the caller, and the
-- database rejects the insert if any is left out.
-- name: InsertInstrument :one
INSERT INTO instruments (
    provider, native_symbol, asset_class, base_asset, quote_asset, contract_type,
    price_scale, qty_scale, tick_size, qty_step_size, contract_size, pip_size,
    supported_intervals, metadata, enabled
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, FALSE
)
RETURNING *;
