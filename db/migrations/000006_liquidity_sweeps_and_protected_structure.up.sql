-- 000006_liquidity_sweeps_and_protected_structure: liquidity sweeps and
-- the sweep-validation tag on structure_events, per
-- docs/strategy/strategy-spec.md v0.2 sections 6-7 (ADR-009, ADR-011,
-- ADR-019).
--
-- liquidity_sweeps is append-only evidence with the exact same
-- supersede-handshake discipline as 000005's confirmed_swings and
-- structure_events (and for the same reason: a partial unique index
-- cannot be DEFERRABLE). A pure retraction (a corrected candle no
-- longer sweeps) stops at superseded_at forever, exactly like a
-- retracted swing.
--
-- trigger_open_time, not trigger_candle_id, is the position key.
-- Candle corrections are themselves a supersede handshake (000003):
-- correcting a candle creates a NEW row with a new id for the same
-- open_time, so a key built from the row id would silently lose track
-- of "current" across a correction to the sweep's own trigger candle --
-- a replay would look up/write the new candle's id and never touch the
-- old sweep row, which would never retract. confirmed_swings already
-- avoids this by keying on pivot_time; trigger_open_time is this
-- table's equivalent. trigger_candle_id is kept as evidence only (which
-- specific candle revision this sweep was computed from), the same
-- role source_candle_ids plays for a swing, never as part of the key.
--
-- Scoping note (same as 000005): trigger_candle_id is a foreign key
-- into candles(id), the canonical 1-minute series. `timeframe` is '1m'
-- for every row this version writes.
CREATE TABLE liquidity_sweeps (
    id                   BIGSERIAL   PRIMARY KEY,
    instrument_id        BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe            TEXT        NOT NULL,
    side                 TEXT        NOT NULL CHECK (side IN ('buy', 'sell')),
    -- buy-side sweeps a swing low; sell-side sweeps a swing high. Which
    -- swing_type swept_swing_id must point at is an application-level
    -- invariant (Engine never constructs the other combination), not a
    -- single-table CHECK: validating it here would require a cross-table
    -- lookup, which a CHECK constraint cannot express.
    swept_swing_id       BIGINT      NOT NULL REFERENCES confirmed_swings(id),
    trigger_open_time    TIMESTAMPTZ NOT NULL,
    trigger_candle_id    BIGINT      NOT NULL REFERENCES candles(id),
    penetration_extreme  BIGINT      NOT NULL, -- quant.Price: the low (buy) or high (sell) that penetrated
    reclaim_close        BIGINT      NOT NULL, -- quant.Price: the same candle's close that reclaimed
    evaluation_time      TIMESTAMPTZ NOT NULL,
    algorithm_version    TEXT        NOT NULL,
    superseded_at        TIMESTAMPTZ,
    superseded_by        BIGINT      REFERENCES liquidity_sweeps(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT liquidity_sweeps_prices_nonnegative CHECK (penetration_extreme >= 0 AND reclaim_close >= 0),
    CONSTRAINT liquidity_sweeps_superseded_consistency CHECK (superseded_by IS NULL OR superseded_at IS NOT NULL)
);

-- At most one current sweep per (position, side): a single candle can
-- in principle sweep both a high and a low (a wide-range candle that
-- takes out both levels and closes back inside both), so the key
-- includes side rather than being trigger_open_time alone.
CREATE UNIQUE INDEX idx_liquidity_sweeps_current
    ON liquidity_sweeps (instrument_id, timeframe, trigger_open_time, side)
    WHERE superseded_at IS NULL;

CREATE OR REPLACE FUNCTION liquidity_sweeps_enforce_lifecycle() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.superseded_by IS NOT NULL THEN
        RAISE EXCEPTION 'liquidity sweep % is superseded and immutable', OLD.id;
    END IF;

    IF NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
       OR NEW.timeframe IS DISTINCT FROM OLD.timeframe
       OR NEW.side IS DISTINCT FROM OLD.side
       OR NEW.swept_swing_id IS DISTINCT FROM OLD.swept_swing_id
       OR NEW.trigger_open_time IS DISTINCT FROM OLD.trigger_open_time
       OR NEW.trigger_candle_id IS DISTINCT FROM OLD.trigger_candle_id
       OR NEW.penetration_extreme IS DISTINCT FROM OLD.penetration_extreme
       OR NEW.reclaim_close IS DISTINCT FROM OLD.reclaim_close
       OR NEW.evaluation_time IS DISTINCT FROM OLD.evaluation_time
       OR NEW.algorithm_version IS DISTINCT FROM OLD.algorithm_version THEN
        RAISE EXCEPTION 'liquidity sweep % evidence is immutable once inserted; a correction appends a new row', OLD.id;
    END IF;

    IF OLD.superseded_at IS NULL THEN
        IF NEW.superseded_at IS NULL OR NEW.superseded_by IS NOT NULL THEN
            RAISE EXCEPTION 'liquidity sweep % is current: the only legal update is setting superseded_at', OLD.id;
        END IF;
    ELSE
        IF NEW.superseded_at IS DISTINCT FROM OLD.superseded_at OR NEW.superseded_by IS NULL THEN
            RAISE EXCEPTION 'liquidity sweep % is already superseded: the only legal update is setting superseded_by', OLD.id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_liquidity_sweeps_enforce_lifecycle
    BEFORE UPDATE ON liquidity_sweeps
    FOR EACH ROW
    EXECUTE FUNCTION liquidity_sweeps_enforce_lifecycle();

-- structure_events: the sweep-validation tag (spec section 7). It is
-- evidence like every other column here, not a derived/computed value
-- read lazily -- so it is immutable from insert and participates in the
-- same correction recompute as the rest of the row, which is why it
-- lives on structure_events itself rather than a side table.
ALTER TABLE structure_events
    ADD COLUMN sweep_validated    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN sweep_event_id     BIGINT  REFERENCES liquidity_sweeps(id),
    ADD COLUMN protected_swing_id BIGINT  REFERENCES confirmed_swings(id);

ALTER TABLE structure_events
    ADD CONSTRAINT structure_events_sweep_tag_consistency CHECK (
        (sweep_validated = false AND sweep_event_id IS NULL AND protected_swing_id IS NULL)
        OR (sweep_validated = true AND sweep_event_id IS NOT NULL AND protected_swing_id IS NOT NULL)
    );

-- Replace 000005's lifecycle function to also guard the three new
-- columns as evidence -- otherwise the trigger would silently allow
-- changing a current row's sweep tag in place, which the rest of this
-- table's design explicitly forbids for every other column.
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
