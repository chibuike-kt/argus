-- 000005_structural_events: confirmed swings and structural direction
-- events (confirmed swing highs/lows, direction establishment, BOS,
-- CHOCH) per docs/strategy/strategy-spec.md v0.1 (ADR-009, ADR-011,
-- ADR-019).
--
-- This is the analysis-side equivalent of the candle store's
-- immutability: append-only, a correction appends a new revision and
-- supersedes the old one, nothing is ever rewritten in place. The
-- supersede handshake and lifecycle triggers below are the exact same
-- pattern as 000003's candles_enforce_lifecycle, for the exact same
-- reason -- Postgres cannot make a partial unique index DEFERRABLE, so
-- "mark old row superseded" and "insert the new row" cannot be a single
-- statement.
--
-- Scoping note: trigger_candle_id and source_candle_ids are foreign keys
-- into candles(id) -- the canonical 1-minute persisted series, nothing
-- else. The `timeframe` column on every table here is a named, explicit
-- dimension (not hardcoded away) so multi-timeframe structure is a real
-- seam, but it is not yet a usable one: there is no aggregated-candle
-- table for a 15m or 1h row to reference, so evaluating structure on a
-- derived timeframe is a later migration (a new evidence table, or a
-- materialized aggregate-candle table to point these FKs at), not a
-- config value you can pass today. `timeframe` is '1m' for every row
-- this version of the engine writes.

-- confirmed_swings: one row per confirmed pivot. Absence of a row means
-- "not a pivot" -- a correction can retract a previously-confirmed
-- swing with no replacement (superseded_at set, superseded_by left NULL
-- forever), which the candle store never does, because every minute
-- always has *some* OHLCV while a pivot may simply stop qualifying.
CREATE TABLE confirmed_swings (
    id                BIGSERIAL   PRIMARY KEY,
    instrument_id     BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe         TEXT        NOT NULL,
    swing_type        TEXT        NOT NULL CHECK (swing_type IN ('high', 'low')),
    pivot_time        TIMESTAMPTZ NOT NULL, -- occurrence: the pivot candle's open_time
    price             BIGINT      NOT NULL, -- quant.Price scaled by the instrument's price_scale
    confirmed_at      TIMESTAMPTZ NOT NULL, -- deterministic: close_time of the last right-hand candle
    evaluation_time   TIMESTAMPTZ NOT NULL, -- when this run of the algorithm recorded the event
    algorithm_version TEXT        NOT NULL,
    source_candle_ids BIGINT[]    NOT NULL, -- the exact fractal-window candle rows, left to right
    superseded_at     TIMESTAMPTZ,
    superseded_by     BIGINT      REFERENCES confirmed_swings(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT confirmed_swings_price_nonnegative CHECK (price >= 0),
    CONSTRAINT confirmed_swings_confirmed_after_pivot CHECK (confirmed_at > pivot_time),
    -- superseded_by implies superseded_at, not the reverse: a pure
    -- retraction sets only superseded_at and stops there, forever.
    CONSTRAINT confirmed_swings_superseded_consistency CHECK (superseded_by IS NULL OR superseded_at IS NOT NULL)
);

-- Exactly one current verdict per position is a database guarantee, the
-- same as idx_candles_current.
CREATE UNIQUE INDEX idx_confirmed_swings_current
    ON confirmed_swings (instrument_id, timeframe, swing_type, pivot_time)
    WHERE superseded_at IS NULL;

CREATE INDEX idx_confirmed_swings_pivot_time
    ON confirmed_swings (instrument_id, timeframe, swing_type, pivot_time DESC)
    WHERE superseded_at IS NULL;

-- A confirmed_swings row has no "forming" state -- it is born
-- already-confirmed -- so this is simpler than candles' lifecycle
-- trigger: evidence is immutable from insert, and the only legal
-- updates are the two supersede-handshake steps.
CREATE OR REPLACE FUNCTION confirmed_swings_enforce_lifecycle() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.superseded_by IS NOT NULL THEN
        RAISE EXCEPTION 'confirmed swing % is superseded and immutable', OLD.id;
    END IF;

    IF NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
       OR NEW.timeframe IS DISTINCT FROM OLD.timeframe
       OR NEW.swing_type IS DISTINCT FROM OLD.swing_type
       OR NEW.pivot_time IS DISTINCT FROM OLD.pivot_time
       OR NEW.price IS DISTINCT FROM OLD.price
       OR NEW.confirmed_at IS DISTINCT FROM OLD.confirmed_at
       OR NEW.evaluation_time IS DISTINCT FROM OLD.evaluation_time
       OR NEW.algorithm_version IS DISTINCT FROM OLD.algorithm_version
       OR NEW.source_candle_ids IS DISTINCT FROM OLD.source_candle_ids THEN
        RAISE EXCEPTION 'confirmed swing % evidence is immutable once inserted; a correction appends a new row', OLD.id;
    END IF;

    IF OLD.superseded_at IS NULL THEN
        -- Current: the only legal update is beginning supersession.
        -- superseded_by stays NULL here -- it either resolves in a
        -- second step (a replacement) or never does (a retraction).
        IF NEW.superseded_at IS NULL OR NEW.superseded_by IS NOT NULL THEN
            RAISE EXCEPTION 'confirmed swing % is current: the only legal update is setting superseded_at', OLD.id;
        END IF;
    ELSE
        -- Already superseded but not yet terminal (a retraction stops
        -- here forever; a replacement takes this second step): the only
        -- legal update is resolving the pointer.
        IF NEW.superseded_at IS DISTINCT FROM OLD.superseded_at OR NEW.superseded_by IS NULL THEN
            RAISE EXCEPTION 'confirmed swing % is already superseded: the only legal update is setting superseded_by', OLD.id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_confirmed_swings_enforce_lifecycle
    BEFORE UPDATE ON confirmed_swings
    FOR EACH ROW
    EXECUTE FUNCTION confirmed_swings_enforce_lifecycle();

-- structure_events: direction establishment, BOS, and CHOCH share one
-- table -- the same shape (a before/after direction, a triggering
-- candle, referenced swings), differentiated by event_type. At most one
-- current event per triggering candle is a real invariant: a single
-- close cannot be both above the bullish reference high and below the
-- bullish reference low in the same direction state.
CREATE TABLE structure_events (
    id                  BIGSERIAL   PRIMARY KEY,
    instrument_id       BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe           TEXT        NOT NULL,
    event_type          TEXT        NOT NULL CHECK (event_type IN ('direction_established', 'bos', 'choch')),
    direction_before    TEXT        NOT NULL CHECK (direction_before IN ('undetermined', 'bullish', 'bearish')),
    direction_after     TEXT        NOT NULL CHECK (direction_after IN ('undetermined', 'bullish', 'bearish')),
    trigger_candle_id   BIGINT      NOT NULL REFERENCES candles(id),
    -- BOS/CHOCH: exactly one id, the broken swing. direction_established:
    -- exactly four, in order [prior_high, latest_high, prior_low,
    -- latest_low]. Each referenced swing carries its own
    -- source_candle_ids, so the full evidence chain is reconstructable
    -- by joining through, not by duplicating candle ids at this level.
    reference_swing_ids BIGINT[]    NOT NULL,
    evaluation_time     TIMESTAMPTZ NOT NULL,
    algorithm_version   TEXT        NOT NULL,
    superseded_at       TIMESTAMPTZ,
    superseded_by       BIGINT      REFERENCES structure_events(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT structure_events_superseded_consistency CHECK (superseded_by IS NULL OR superseded_at IS NOT NULL),
    -- Tightened per review: CHOCH must be exactly bullish<->bearish, not
    -- merely "different from before" (which would also admit a flip to
    -- 'undetermined', which should never happen for a CHOCH).
    CONSTRAINT structure_events_direction_shape CHECK (
        (event_type = 'bos' AND direction_before = direction_after AND direction_before IN ('bullish', 'bearish'))
        OR (event_type = 'choch' AND (
            (direction_before = 'bullish' AND direction_after = 'bearish')
            OR (direction_before = 'bearish' AND direction_after = 'bullish')
        ))
        OR (event_type = 'direction_established' AND direction_before = 'undetermined' AND direction_after IN ('bullish', 'bearish'))
    )
);

CREATE UNIQUE INDEX idx_structure_events_current
    ON structure_events (instrument_id, timeframe, trigger_candle_id)
    WHERE superseded_at IS NULL;

-- Same shape as confirmed_swings' trigger: evidence immutable from
-- insert, only the two handshake steps are ever legal updates.
CREATE OR REPLACE FUNCTION structure_events_enforce_lifecycle() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.superseded_by IS NOT NULL THEN
        RAISE EXCEPTION 'structure event % is superseded and immutable', OLD.id;
    END IF;

    IF NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
       OR NEW.timeframe IS DISTINCT FROM OLD.timeframe
       OR NEW.event_type IS DISTINCT FROM OLD.event_type
       OR NEW.direction_before IS DISTINCT FROM OLD.direction_before
       OR NEW.direction_after IS DISTINCT FROM OLD.direction_after
       OR NEW.trigger_candle_id IS DISTINCT FROM OLD.trigger_candle_id
       OR NEW.reference_swing_ids IS DISTINCT FROM OLD.reference_swing_ids
       OR NEW.evaluation_time IS DISTINCT FROM OLD.evaluation_time
       OR NEW.algorithm_version IS DISTINCT FROM OLD.algorithm_version THEN
        RAISE EXCEPTION 'structure event % evidence is immutable once inserted; a correction appends a new row', OLD.id;
    END IF;

    IF OLD.superseded_at IS NULL THEN
        IF NEW.superseded_at IS NULL OR NEW.superseded_by IS NOT NULL THEN
            RAISE EXCEPTION 'structure event % is current: the only legal update is setting superseded_at', OLD.id;
        END IF;
    ELSE
        IF NEW.superseded_at IS DISTINCT FROM OLD.superseded_at OR NEW.superseded_by IS NULL THEN
            RAISE EXCEPTION 'structure event % is already superseded: the only legal update is setting superseded_by', OLD.id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_structure_events_enforce_lifecycle
    BEFORE UPDATE ON structure_events
    FOR EACH ROW
    EXECUTE FUNCTION structure_events_enforce_lifecycle();

-- structure_state: the maintained direction state the spec describes
-- ("an event-driven state, not recomputed on every pivot") -- a pure,
-- mutable PROJECTION, not a source of truth. It is always fully
-- reconstructable by replaying confirmed_swings + structure_events in
-- order; a correction's recompute rebuilds the relevant slice of it by
-- reconstructing the as-of state from the event log, not through any
-- separate "rewind" bookkeeping of its own.
CREATE TABLE structure_state (
    instrument_id            BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe                TEXT        NOT NULL,
    direction                TEXT        NOT NULL CHECK (direction IN ('undetermined', 'bullish', 'bearish')),
    reference_swing_high_id  BIGINT      REFERENCES confirmed_swings(id),
    reference_swing_low_id   BIGINT      REFERENCES confirmed_swings(id),
    last_event_id            BIGINT      REFERENCES structure_events(id),
    last_processed_candle_id BIGINT      REFERENCES candles(id),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (instrument_id, timeframe)
);
