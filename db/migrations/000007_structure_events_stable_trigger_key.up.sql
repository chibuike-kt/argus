-- 000007_structure_events_stable_trigger_key: fixes the same keying
-- flaw already fixed for liquidity_sweeps in 000006, here for
-- structure_events (ADR-020 expand-contract: this column predates its
-- own migration having existed, hence the backfill).
--
-- structure_events' current-row uniqueness and lookup were keyed on
-- trigger_candle_id, a row id. A candle correction mints a NEW row
-- with a new id for the same open_time (the same supersede handshake
-- as every other corrected table here) -- so a correction to a
-- candle that is ITSELF a BOS or CHOCH's trigger would make the
-- replay look up/write against the new id and never touch the old
-- "current" event, which would never retract. Two "current" events
-- for the same position could then coexist, since (old_trigger_id)
-- and (new_trigger_id) don't collide on the old unique index.
-- confirmed_swings already avoids this by keying on pivot_time, a
-- stable position, not a row id; trigger_open_time is this table's
-- equivalent. trigger_candle_id is kept as revision-pinned evidence
-- only (the same role source_candle_ids plays for a swing), never
-- again as part of the key.
ALTER TABLE structure_events ADD COLUMN trigger_open_time TIMESTAMPTZ;

-- The lifecycle trigger (still in its 000006 shape at this point) only
-- ever permits a current row's own two handshake steps -- any other
-- update, including this backfill, is rejected by its own design.
-- Disabled only for the backfill, re-enabled immediately after.
ALTER TABLE structure_events DISABLE TRIGGER trg_structure_events_enforce_lifecycle;

UPDATE structure_events se
SET trigger_open_time = c.open_time
FROM candles c
WHERE c.id = se.trigger_candle_id;

ALTER TABLE structure_events ENABLE TRIGGER trg_structure_events_enforce_lifecycle;

ALTER TABLE structure_events ALTER COLUMN trigger_open_time SET NOT NULL;

DROP INDEX idx_structure_events_current;
CREATE UNIQUE INDEX idx_structure_events_current
    ON structure_events (instrument_id, timeframe, trigger_open_time)
    WHERE superseded_at IS NULL;

-- Replace the lifecycle function to treat trigger_open_time as
-- immutable evidence too, same as every other column here.
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
       OR NEW.trigger_open_time IS DISTINCT FROM OLD.trigger_open_time
       OR NEW.trigger_candle_id IS DISTINCT FROM OLD.trigger_candle_id
       OR NEW.reference_swing_ids IS DISTINCT FROM OLD.reference_swing_ids
       OR NEW.evaluation_time IS DISTINCT FROM OLD.evaluation_time
       OR NEW.algorithm_version IS DISTINCT FROM OLD.algorithm_version
       OR NEW.sweep_validated IS DISTINCT FROM OLD.sweep_validated
       OR NEW.sweep_event_id IS DISTINCT FROM OLD.sweep_event_id
       OR NEW.protected_swing_id IS DISTINCT FROM OLD.protected_swing_id THEN
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
