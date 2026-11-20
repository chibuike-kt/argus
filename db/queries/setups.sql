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
    evaluation_time, algorithm_version, updated_at, found_mode
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
    poi_source_candle_revision, evaluation_time, algorithm_version, evidence
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- ListSetupsByInstrument lists every setup for (instrument_id,
-- timeframe), most recently updated first -- cmd/argus-worker's own
-- read path (processInstrument has no reason to filter or cap this;
-- it is not the HTTP layer -- see ListSetupsByInstrumentFiltered for
-- that one).
-- name: ListSetupsByInstrument :many
SELECT * FROM setups
WHERE instrument_id = $1 AND timeframe = $2
ORDER BY updated_at DESC;

-- ListSetupsByInstrumentFiltered is GET /setups' own read path
-- (internal/httpapi, worker-hardening brief Part C): same ordering as
-- ListSetupsByInstrument (most recently updated first -- unchanged,
-- not something this query introduces), plus an optional state filter
-- (sqlc::text passed NULL means "no filter", not a fourth state value
-- to match against) and a required LIMIT, enforced in the query
-- itself for the same reason ListClosedCandlesInRangeLimited's own
-- comment gives.
-- name: ListSetupsByInstrumentFiltered :many
SELECT * FROM setups
WHERE instrument_id = $1 AND timeframe = $2
  AND (sqlc.narg('state')::text IS NULL OR state = sqlc.narg('state')::text)
ORDER BY updated_at DESC
LIMIT $3;

-- ListNonTerminalSetupsForBreachCheck is InvalidateBreached's own read
-- path (internal/setups, spec v0.12.1): every non-terminal setup
-- (detected, actionable, triggered -- invalidated is terminal and
-- excluded here, not just guarded again downstream) for (instrument_id,
-- timeframe), joined to its own POI formation candle's open_time so the
-- caller can enforce "the breaching candle must be strictly later than
-- the setup's POI formation candle" without a second round trip per
-- setup. Ordered by id for determinism, matching this package's other
-- per-instrument list queries.
-- name: ListNonTerminalSetupsForBreachCheck :many
SELECT s.*, c.open_time AS poi_open_time
FROM setups s
JOIN candles c ON c.id = s.poi_source_candle_id
WHERE s.instrument_id = $1 AND s.timeframe = $2
  AND s.state IN ('detected', 'actionable', 'triggered')
ORDER BY s.id;

-- ListEventsForSetup returns a setup's full history in creation order
-- -- reconstructing a setup's story (the frontend's eventual "Evidence"
-- list is exactly this kind of data).
-- name: ListEventsForSetup :many
SELECT * FROM setup_events
WHERE setup_id = $1
ORDER BY id ASC;

-- ListSetupLifecycle summarises each setup's own event history for the
-- chart (Phase 6): when it was detected, triggered and invalidated (the
-- events' evaluation times, so a zone ends at its real invalidation, or
-- is still active), and whether any of its events recorded evidence. One
-- statement for the whole list, never one per setup. A time is NULL when
-- that event never happened (its has_* flag is false; the time column
-- then holds the epoch and must not be read).
-- name: ListSetupLifecycle :many
SELECT
    e.setup_id,
    bool_or(e.event_type = 'detected')::boolean AS has_detected,
    coalesce(min(e.evaluation_time) FILTER (WHERE e.event_type = 'detected'), 'epoch'::timestamptz)::timestamptz AS detected_at,
    bool_or(e.event_type = 'triggered')::boolean AS has_triggered,
    coalesce(min(e.evaluation_time) FILTER (WHERE e.event_type = 'triggered'), 'epoch'::timestamptz)::timestamptz AS triggered_at,
    bool_or(e.event_type = 'invalidated')::boolean AS has_invalidated,
    coalesce(min(e.evaluation_time) FILTER (WHERE e.event_type = 'invalidated'), 'epoch'::timestamptz)::timestamptz AS invalidated_at,
    bool_or(e.evidence <> 'null'::jsonb)::boolean AS evidence_recorded
FROM setup_events e
WHERE e.setup_id = ANY(sqlc.arg(setup_ids)::bigint[])
GROUP BY e.setup_id;

-- GetLatestSetupEvidence returns the newest evidence a setup's events
-- recorded (the detected event's, or a later amendment's after a candle
-- correction). No row: no evidence was recorded for this setup.
-- name: GetLatestSetupEvidence :one
SELECT id, event_type, evaluation_time, evidence
FROM setup_events
WHERE setup_id = $1 AND evidence <> 'null'::jsonb
ORDER BY id DESC
LIMIT 1;

-- ListLatestSetupEvidence is GetLatestSetupEvidence for many setups at
-- once (GET /setups?include_evidence=true): each setup's newest recorded
-- evidence, one statement. A setup with none recorded is absent.
-- name: ListLatestSetupEvidence :many
SELECT DISTINCT ON (setup_id) setup_id, id, event_type, evaluation_time, evidence
FROM setup_events
WHERE setup_id = ANY(sqlc.arg(setup_ids)::bigint[]) AND evidence <> 'null'::jsonb
ORDER BY setup_id, id DESC;
