DROP TABLE IF EXISTS structure_state;

DROP TRIGGER IF EXISTS trg_structure_events_enforce_lifecycle ON structure_events;
DROP FUNCTION IF EXISTS structure_events_enforce_lifecycle();
DROP TABLE IF EXISTS structure_events;

DROP TRIGGER IF EXISTS trg_confirmed_swings_enforce_lifecycle ON confirmed_swings;
DROP FUNCTION IF EXISTS confirmed_swings_enforce_lifecycle();
DROP TABLE IF EXISTS confirmed_swings;
