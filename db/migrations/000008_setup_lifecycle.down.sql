DROP TRIGGER IF EXISTS trg_setup_events_prevent_update ON setup_events;
DROP FUNCTION IF EXISTS setup_events_prevent_update();

ALTER TABLE setups DROP CONSTRAINT IF EXISTS setups_last_event_id_fkey;

DROP TABLE IF EXISTS setup_events;
DROP TABLE IF EXISTS setups;
