-- 000024: gap identity per interval, (instrument_id, interval, gap_start,
-- gap_end), replacing 000003's UNIQUE (instrument_id, gap_start, gap_end)
-- once 000025 drops it: a 1 minute gap and a 1 hour gap may cover the same
-- range. Built CONCURRENTLY, alone in its file, as in 000022.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS candle_gaps_identity_by_interval
    ON candle_gaps (instrument_id, interval, gap_start, gap_end);
