-- Reverse of 000006, in dependency order: restore structure_events'
-- lifecycle trigger to its 000005 shape before dropping the columns it
-- referenced, then drop the sweep-tag columns, then liquidity_sweeps.

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

ALTER TABLE structure_events DROP CONSTRAINT structure_events_sweep_tag_consistency;
ALTER TABLE structure_events DROP COLUMN sweep_validated;
ALTER TABLE structure_events DROP COLUMN sweep_event_id;
ALTER TABLE structure_events DROP COLUMN protected_swing_id;

ALTER TABLE structure_state DROP CONSTRAINT structure_state_protected_anchor_consistency;
ALTER TABLE structure_state DROP COLUMN protected_swing_id;
ALTER TABLE structure_state DROP COLUMN protected_swing_side;

DROP TRIGGER IF EXISTS trg_liquidity_sweeps_enforce_lifecycle ON liquidity_sweeps;
DROP FUNCTION IF EXISTS liquidity_sweeps_enforce_lifecycle();
DROP TABLE IF EXISTS liquidity_sweeps;
