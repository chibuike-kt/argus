-- name: GetStructureState :one
SELECT * FROM structure_state WHERE instrument_id = $1 AND timeframe = $2;

-- SetStructureState is a plain upsert: structure_state is a mutable
-- projection, not an evidence record, so it is the one table in this
-- migration that is written in place rather than through a supersede
-- handshake (000005's comment on the table explains why).
-- name: SetStructureState :one
INSERT INTO structure_state (
    instrument_id, timeframe, direction, reference_swing_high_id,
    reference_swing_low_id, last_event_id, last_processed_candle_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (instrument_id, timeframe) DO UPDATE SET
    direction = EXCLUDED.direction,
    reference_swing_high_id = EXCLUDED.reference_swing_high_id,
    reference_swing_low_id = EXCLUDED.reference_swing_low_id,
    last_event_id = EXCLUDED.last_event_id,
    last_processed_candle_id = EXCLUDED.last_processed_candle_id,
    updated_at = EXCLUDED.updated_at
RETURNING *;
