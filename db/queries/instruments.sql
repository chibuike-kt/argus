-- name: GetInstrument :one
SELECT * FROM instruments WHERE id = $1;

-- name: ListEnabledInstruments :many
SELECT * FROM instruments WHERE enabled = TRUE ORDER BY provider, native_symbol;

-- name: CreateInstrument :one
INSERT INTO instruments (provider, native_symbol, asset_class, base_asset, quote_asset, contract_type, price_precision, quantity_precision, tick_size, supported_intervals, enabled, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;
