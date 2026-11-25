-- 000024: gap identity per interval, (instrument_id, interval, gap_start,
-- gap_end), replacing 000003's UNIQUE (instrument_id, gap_start, gap_end)
-- once 000025 drops it: a 1 minute gap and a 1 hour gap may cover the same
-- range. Built CONCURRENTLY, alone in its file, as in 000022.
--
-- No IF NOT EXISTS, on purpose: a failed CONCURRENTLY build leaves an
-- INVALID index of this name behind, and IF NOT EXISTS would skip it on a
-- retry and record the migration as applied over an index that enforces
-- nothing. Without it, a retry fails with "already exists" until the
-- leftover is dropped (DROP INDEX CONCURRENTLY <name>) and the dirty
-- version is forced back, so an invalid index can never pass for a built
-- one.
CREATE UNIQUE INDEX CONCURRENTLY candle_gaps_identity_by_interval
    ON candle_gaps (instrument_id, interval, gap_start, gap_end);
