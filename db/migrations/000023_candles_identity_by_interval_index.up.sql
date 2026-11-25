-- 000023: full candle identity per interval, (instrument_id, interval,
-- open_time, revision), replacing 000003's UNIQUE (instrument_id, open_time,
-- revision) once 000025 drops it. Built CONCURRENTLY, alone in its file, for
-- the same reason as 000022.
--
-- No IF NOT EXISTS, on purpose: a failed CONCURRENTLY build leaves an
-- INVALID index of this name behind, and IF NOT EXISTS would skip it on a
-- retry and record the migration as applied over an index that enforces
-- nothing. Without it, a retry fails with "already exists" until the
-- leftover is dropped (DROP INDEX CONCURRENTLY <name>) and the dirty
-- version is forced back, so an invalid index can never pass for a built
-- one.
CREATE UNIQUE INDEX CONCURRENTLY candles_identity_by_interval
    ON candles (instrument_id, interval, open_time, revision);
