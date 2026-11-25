-- 000022: the current-row index per interval, replacing 000003's
-- idx_candles_current (instrument_id, open_time) once 000025 drops it.
-- Exactly one current row per (instrument, interval, open_time).
--
-- CONCURRENTLY, so building it on a large candles table does not block
-- writes (ADR-020 expand step). CREATE INDEX CONCURRENTLY cannot run inside
-- a transaction, so this file holds this one statement only (golang-migrate
-- runs a single statement without a wrapping transaction). If the build
-- fails it leaves an INVALID index and a dirty migration version: drop the
-- index and force the version back before retrying.
--
-- No IF NOT EXISTS, on purpose: a failed CONCURRENTLY build leaves an
-- INVALID index of this name behind, and IF NOT EXISTS would skip it on a
-- retry and record the migration as applied over an index that enforces
-- nothing. Without it, a retry fails with "already exists" until the
-- leftover is dropped (DROP INDEX CONCURRENTLY <name>) and the dirty
-- version is forced back, so an invalid index can never pass for a built
-- one.
CREATE UNIQUE INDEX CONCURRENTLY idx_candles_current_by_interval
    ON candles (instrument_id, interval, open_time)
    WHERE superseded_at IS NULL;
