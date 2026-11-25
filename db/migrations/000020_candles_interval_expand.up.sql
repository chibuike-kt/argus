-- 000020_candles_interval_expand: native higher timeframe candles, the
-- expand step (L1.1; ADR-006 "Higher timeframe candles", decision D11;
-- ADR-020 expand-contract).
--
-- candles and candle_gaps get an interval column. Every existing row is a
-- 1 minute row, so the column defaults to '1m': adding a NOT NULL column
-- with a constant default is a catalog-only change in Postgres 11+ (no
-- table rewrite), and old code that never names the column keeps writing
-- valid 1 minute rows.
--
-- Allowed intervals: 1m (the canonical live series), and the native 15m,
-- 1h and 4h series kept from the provider. 1d is later; 5m stays derived
-- from 1m and is never stored (ADR-006 amendment of 22 November 2026).
--
-- The new CHECK constraints are added NOT VALID here (a brief lock, no
-- scan) and validated in 000021, in their own transaction, so the full
-- scan does not run under this migration's ACCESS EXCLUSIVE lock.

-- candle_interval_seconds maps an allowed interval to its length in
-- seconds, and NULL for anything else (which the interval_allowed checks
-- reject first). IMMUTABLE: a pure mapping, safe inside CHECK constraints.
CREATE FUNCTION candle_interval_seconds(i TEXT) RETURNS INTEGER
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT CASE i
        WHEN '1m'  THEN 60
        WHEN '15m' THEN 900
        WHEN '1h'  THEN 3600
        WHEN '4h'  THEN 14400
    END
$$;

ALTER TABLE candles ADD COLUMN interval TEXT NOT NULL DEFAULT '1m';

ALTER TABLE candles ADD CONSTRAINT candles_interval_allowed
    CHECK (interval IN ('1m', '15m', '1h', '4h')) NOT VALID;

-- Generalises 000003's candles_duration_one_minute to every interval.
ALTER TABLE candles ADD CONSTRAINT candles_duration_matches_interval
    CHECK (close_time = open_time + candle_interval_seconds(interval) * INTERVAL '1 second') NOT VALID;

-- Generalises 000003's candles_open_time_minute_aligned. Epoch modulo, never
-- date_trunc: extract(epoch FROM timestamptz) is the absolute instant, so
-- the result is the same in every session time zone (a 4h candle opens at
-- 00, 04, 08, 12, 16 or 20 UTC whatever the session's TimeZone says).
ALTER TABLE candles ADD CONSTRAINT candles_open_time_interval_aligned
    CHECK (mod(extract(epoch FROM open_time), candle_interval_seconds(interval)) = 0) NOT VALID;

ALTER TABLE candle_gaps ADD COLUMN interval TEXT NOT NULL DEFAULT '1m';

ALTER TABLE candle_gaps ADD CONSTRAINT candle_gaps_interval_allowed
    CHECK (interval IN ('1m', '15m', '1h', '4h')) NOT VALID;
