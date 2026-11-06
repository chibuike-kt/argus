-- 000008_setup_lifecycle: the persisted setup lifecycle and its
-- immutable event log, per docs/strategy/strategy-spec.md section 15
-- (ADR-010, ADR-011). Sections 1-14 are pure, stateless classifications
-- with no persistence of their own; this is the first migration that
-- gives a setup an identity and a history.
--
-- setup_events is declared before setups because setups.last_event_id
-- points at it -- but setup_events.setup_id points back at setups, so
-- the two tables are genuinely circular. Resolved the standard way:
-- setups is created first with last_event_id as a plain column (no FK
-- yet), then setup_events, then the FK is added to setups by ALTER
-- TABLE once setup_events exists. structure_state/structure_events
-- (000005) never needed this because structure_events does not
-- reference structure_state back.

-- setups: the mutable current-state projection, the same role
-- structure_state plays for direction (000005's comment) -- written in
-- place via upsert, never through a supersede handshake, because this
-- is a projection, not evidence. Unlike structure_state, which has
-- exactly one row per (instrument_id, timeframe) (there is only ever
-- one current direction), setups has one row per IDENTITY (spec
-- section 15.3), and many identities can be live for the same
-- (instrument_id, timeframe) at once -- so the unique index below is
-- the full identity tuple, not just (instrument_id, timeframe).
--
-- A setup's identity is never superseded the way a swing or structure-
-- event POSITION is: spec section 15.3 is explicit that an identity
-- which reaches invalidated simply stays invalidated forever, and a
-- genuinely new setup (a different source candle) gets a new identity,
-- a new row. So, unlike idx_structure_events_current, this unique
-- index carries no superseded_at partial condition -- there is no
-- superseded_at column on this table at all.
CREATE TABLE setups (
    id                          BIGSERIAL   PRIMARY KEY,
    instrument_id               BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe                   TEXT        NOT NULL,
    -- Identity (spec section 15.3): instrument_id, timeframe, poi_type,
    -- poi_source_candle_id, direction, strategy_version. Candle
    -- REVISION is deliberately excluded from identity -- a corrected
    -- candle amends the existing row (see poi_source_candle_revision
    -- below), it never spawns a new identity.
    poi_type                    TEXT        NOT NULL CHECK (poi_type IN ('order_block', 'qmr')),
    poi_source_candle_id        BIGINT      NOT NULL REFERENCES candles(id),
    direction                   TEXT        NOT NULL CHECK (direction IN ('bullish', 'bearish')),
    strategy_version             TEXT        NOT NULL,

    -- Current lifecycle state (spec section 15.2). Expired/Cancelled are
    -- deliberately not in this list -- v1 does not implement them (see
    -- the spec section's own "not implemented in v1" note).
    state                       TEXT        NOT NULL CHECK (state IN ('detected', 'actionable', 'triggered', 'invalidated')),
    -- The POI's source-candle revision this row currently reflects --
    -- NOT part of identity (see above). A later evaluation step whose
    -- POI resolves to the same source candle at a DIFFERENT revision is
    -- an amendment (spec section 15.4), detected by comparing against
    -- this column.
    poi_source_candle_revision  INT         NOT NULL,
    -- Full analysis.TradePlan snapshot (status, direction, entry, stop,
    -- targets with their RR, the POI it carries, the protected-swing
    -- anchor) as of the most recent reconciliation. JSONB, not fixed
    -- columns, because targets are a variable-length list of (level,
    -- RR) pairs (ADR-012) -- the one place in this schema that varies
    -- in shape the way every other evidence row here does not.
    plan_snapshot                JSONB       NOT NULL,
    -- FK to setup_events added below, once that table exists (see the
    -- circular-reference note above).
    last_event_id                BIGINT,
    evaluation_time               TIMESTAMPTZ NOT NULL,
    algorithm_version             TEXT        NOT NULL,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_setups_identity
    ON setups (instrument_id, timeframe, poi_type, poi_source_candle_id, direction, strategy_version);

CREATE INDEX idx_setups_instrument_state
    ON setups (instrument_id, timeframe, state);

-- setup_events: the append-only log, the same role confirmed_swings
-- plays for swings (000005's comment) -- but simpler. Per spec section
-- 15.4, nothing here is ever retracted or replaced, only appended: a
-- correction to the same identity appends an 'amendment' event rather
-- than superseding a prior one. So this table needs none of
-- confirmed_swings' or structure_events' supersede-handshake machinery
-- (no superseded_at/superseded_by, no lifecycle trigger permitting two
-- handshake steps) -- only a guard that no row, once inserted, is ever
-- updated at all.
CREATE TABLE setup_events (
    id                          BIGSERIAL   PRIMARY KEY,
    setup_id                     BIGINT      NOT NULL REFERENCES setups(id),
    event_type                   TEXT        NOT NULL CHECK (event_type IN ('detected', 'actionable', 'triggered', 'invalidated', 'amendment')),
    plan_snapshot                 JSONB       NOT NULL,
    poi_source_candle_id          BIGINT      NOT NULL REFERENCES candles(id),
    poi_source_candle_revision    INT         NOT NULL,
    evaluation_time                TIMESTAMPTZ NOT NULL,
    algorithm_version              TEXT        NOT NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_setup_events_setup_id ON setup_events (setup_id, id);

ALTER TABLE setups
    ADD CONSTRAINT setups_last_event_id_fkey FOREIGN KEY (last_event_id) REFERENCES setup_events(id);

-- setup_events is immutable from insert: every legal change to a
-- setup's story is a new row (spec section 15.4, ADR-011), never an
-- update to an existing one. Defense-in-depth, the same reason every
-- other evidence table in this schema has a trigger, even though
-- nothing in this package's Go code ever issues an UPDATE against this
-- table today.
CREATE OR REPLACE FUNCTION setup_events_prevent_update() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'setup event % is immutable once inserted; a correction or transition appends a new row', OLD.id;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_setup_events_prevent_update
    BEFORE UPDATE ON setup_events
    FOR EACH ROW
    EXECUTE FUNCTION setup_events_prevent_update();
