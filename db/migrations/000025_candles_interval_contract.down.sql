-- Restores 000003's 1 minute only constraints. They cannot hold while
-- native 15m, 1h or 4h rows exist (a 1h row at 13:00 collides with the 1m
-- row at 13:00 under the old identity, and fails the one minute duration
-- check), so this down step DELETES every non 1m candle and gap first.
-- That is provider data the worker can fetch again (the first-run backfill
-- re-reads the provider's whole retention), not a decision record; nothing
-- outside candles references a non 1m candle before analysis runs on the
-- native series (L2). Every superseded_by pointer among the deleted rows
-- points at another deleted row, so one DELETE statement satisfies the
-- self-reference at statement end.
DELETE FROM candle_gaps WHERE interval <> '1m';
DELETE FROM candles WHERE interval <> '1m';

ALTER TABLE candle_gaps ADD CONSTRAINT candle_gaps_instrument_id_gap_start_gap_end_key
    UNIQUE (instrument_id, gap_start, gap_end);
ALTER TABLE candles ADD CONSTRAINT candles_open_time_minute_aligned CHECK (
    (date_trunc('minute', open_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') = open_time
);
ALTER TABLE candles ADD CONSTRAINT candles_duration_one_minute
    CHECK (close_time = open_time + INTERVAL '1 minute');
ALTER TABLE candles ADD CONSTRAINT candles_instrument_id_open_time_revision_key
    UNIQUE (instrument_id, open_time, revision);
CREATE UNIQUE INDEX idx_candles_current ON candles (instrument_id, open_time)
    WHERE superseded_at IS NULL;
