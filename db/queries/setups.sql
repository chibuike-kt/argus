-- name: GetSetupByIdentity :one
SELECT * FROM setups
WHERE instrument_id = $1 AND timeframe = $2 AND poi_type = $3
  AND poi_source_candle_id = $4 AND direction = $5 AND strategy_version = $6;

-- name: GetSetupByID :one
SELECT * FROM setups WHERE id = $1;

-- InsertSetup creates the row for a brand-new identity (spec section
-- 15.1: the first step whose TradePlan.Status != PlanNone for this
-- identity). last_event_id is set afterward, by UpdateSetup, once the
-- paired 'detected' setup_events row exists -- the same reason
-- structure_state's own last_event_id is set in a later call than the
-- row that triggered it.
-- name: InsertSetup :one
INSERT INTO setups (
    instrument_id, timeframe, poi_type, poi_source_candle_id, direction,
    strategy_version, state, poi_source_candle_revision, plan_snapshot,
    evaluation_time, algorithm_version, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- UpdateSetup is setups' one and only write path once a row exists:
-- it is a mutable projection (000008's comment on the table), so a
-- transition and an amendment both go through this same plain update,
-- never a supersede handshake. Callers set state unchanged for an
-- amendment, and a new state for a lifecycle transition.
-- name: UpdateSetup :one
UPDATE setups SET
    state = $2,
    poi_source_candle_revision = $3,
    plan_snapshot = $4,
    last_event_id = $5,
    evaluation_time = $6,
    algorithm_version = $7,
    updated_at = $8
WHERE id = $1
RETURNING *;

-- name: InsertSetupEvent :one
INSERT INTO setup_events (
    setup_id, event_type, plan_snapshot, poi_source_candle_id,
    poi_source_candle_revision, evaluation_time, algorithm_version
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- ListSetupsByInstrument lists every setup for (instrument_id,
-- timeframe), most recently updated first -- for whatever reads the
-- worker or a future API need (no HTTP surface yet, per this brief).
-- name: ListSetupsByInstrument :many
SELECT * FROM setups
WHERE instrument_id = $1 AND timeframe = $2
ORDER BY updated_at DESC;

-- ListEventsForSetup returns a setup's full history in creation order
-- -- reconstructing a setup's story (the frontend's eventual "Evidence"
-- list is exactly this kind of data).
-- name: ListEventsForSetup :many
SELECT * FROM setup_events
WHERE setup_id = $1
ORDER BY id ASC;
