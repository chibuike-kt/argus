-- name: GetLatestQuote :one
SELECT * FROM latest_quotes WHERE instrument_id = $1;

-- UpsertLatestQuote rejects an out-of-order quote rather than letting it
-- silently overwrite a newer one: the WHERE clause on DO UPDATE only fires
-- when the incoming quote_time is at least as new as what is stored. When
-- it is older, Postgres treats the conflict like DO NOTHING and RETURNING
-- yields no row, which the store layer reads as "stale, skipped" rather
-- than as success (guide 16: an upstream data-feed quirk must not
-- silently become current, accepted data).
-- name: UpsertLatestQuote :one
INSERT INTO latest_quotes (instrument_id, bid, ask, quote_time, ingested_at, provider)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (instrument_id) DO UPDATE SET
    bid = EXCLUDED.bid,
    ask = EXCLUDED.ask,
    quote_time = EXCLUDED.quote_time,
    ingested_at = EXCLUDED.ingested_at,
    provider = EXCLUDED.provider
WHERE EXCLUDED.quote_time >= latest_quotes.quote_time
RETURNING *;
