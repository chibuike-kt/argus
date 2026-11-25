-- 000023: full candle identity per interval, (instrument_id, interval,
-- open_time, revision), replacing 000003's UNIQUE (instrument_id, open_time,
-- revision) once 000025 drops it. Built CONCURRENTLY, alone in its file, for
-- the same reason as 000022.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS candles_identity_by_interval
    ON candles (instrument_id, interval, open_time, revision);
