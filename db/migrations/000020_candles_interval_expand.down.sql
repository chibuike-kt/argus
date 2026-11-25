ALTER TABLE candle_gaps DROP CONSTRAINT IF EXISTS candle_gaps_interval_allowed;
ALTER TABLE candle_gaps DROP COLUMN IF EXISTS interval;

ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_open_time_interval_aligned;
ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_duration_matches_interval;
ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_interval_allowed;
ALTER TABLE candles DROP COLUMN IF EXISTS interval;

DROP FUNCTION IF EXISTS candle_interval_seconds(TEXT);
