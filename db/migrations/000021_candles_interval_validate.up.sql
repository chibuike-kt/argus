-- 000021_candles_interval_validate: validates 000020's NOT VALID checks in
-- their own transaction. VALIDATE CONSTRAINT takes SHARE UPDATE EXCLUSIVE,
-- which does not block reads or writes, while it scans the table (ADR-020
-- expand-contract on a large table).
ALTER TABLE candles VALIDATE CONSTRAINT candles_interval_allowed;
ALTER TABLE candles VALIDATE CONSTRAINT candles_duration_matches_interval;
ALTER TABLE candles VALIDATE CONSTRAINT candles_open_time_interval_aligned;
ALTER TABLE candle_gaps VALIDATE CONSTRAINT candle_gaps_interval_allowed;
