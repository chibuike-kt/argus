-- name: GetProcessingCursor :one
SELECT * FROM processing_cursors WHERE instrument_id = $1 AND timeframe = $2;

-- SetProcessingCursor is a plain upsert: processing_cursors is a
-- mutable projection, not an evidence record (000009's comment on the
-- table explains why), so it is written in place rather than through
-- a supersede handshake, the same pattern SetStructureState uses.
-- name: SetProcessingCursor :one
INSERT INTO processing_cursors (instrument_id, timeframe, last_processed_close_time, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (instrument_id, timeframe) DO UPDATE SET
    last_processed_close_time = EXCLUDED.last_processed_close_time,
    updated_at = EXCLUDED.updated_at
RETURNING *;
