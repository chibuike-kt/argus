-- 000025_candles_interval_contract: drops 000003's 1 minute only
-- constraints and indexes, now replaced by 000020 to 000024 (ADR-020
-- contract step).
--
-- Shipped in the same release as the expand steps, not a later one: the
-- old unique index (instrument_id, open_time) and the old identity
-- constraint (instrument_id, open_time, revision) make a 15m, 1h or 4h row
-- at the same open_time as a 1m row impossible, so the native series
-- cannot be written until they are gone. Application code from before this
-- release uses ON CONFLICT (instrument_id, open_time), which needs the old
-- index: rolling the application back past this migration requires
-- migrating this step down first (see its down file).
--
-- Each DROP is a catalog change under a brief lock; nothing is scanned.
DROP INDEX IF EXISTS idx_candles_current;
ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_instrument_id_open_time_revision_key;
ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_duration_one_minute;
ALTER TABLE candles DROP CONSTRAINT IF EXISTS candles_open_time_minute_aligned;
ALTER TABLE candle_gaps DROP CONSTRAINT IF EXISTS candle_gaps_instrument_id_gap_start_gap_end_key;
