-- name: GetInstrumentByID :one
SELECT * FROM instruments WHERE id = $1;

-- name: GetInstrumentByProviderAndSymbol :one
SELECT * FROM instruments WHERE provider = $1 AND native_symbol = $2;

-- name: ListEnabledInstruments :many
SELECT * FROM instruments WHERE enabled = TRUE ORDER BY provider, native_symbol;

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
