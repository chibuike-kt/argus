-- 000002_instrument_sizing_and_scale: adds quant.Scale-based price/quantity
-- precision, tick/step size and contract conventions to instruments
-- (ADR-006, ADR-012). Expand step of an expand-contract migration
-- (ADR-020): the old NUMERIC tick_size is renamed out of the way rather
-- than dropped, so nothing currently reading it breaks; a later contract
-- migration drops tick_size_deprecated once nothing reads it.

ALTER TABLE instruments
    RENAME COLUMN tick_size TO tick_size_deprecated;

ALTER TABLE instruments
    ADD COLUMN price_scale   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN qty_scale     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN tick_size     BIGINT  NOT NULL DEFAULT 0,
    ADD COLUMN qty_step_size BIGINT  NOT NULL DEFAULT 0,
    ADD COLUMN contract_size BIGINT,
    ADD COLUMN pip_size      BIGINT;

ALTER TABLE instruments
    ADD CONSTRAINT instruments_price_scale_range CHECK (price_scale BETWEEN 0 AND 12),
    ADD CONSTRAINT instruments_qty_scale_range CHECK (qty_scale BETWEEN 0 AND 12),
    ADD CONSTRAINT instruments_tick_size_nonnegative CHECK (tick_size >= 0),
    ADD CONSTRAINT instruments_qty_step_size_nonnegative CHECK (qty_step_size >= 0),
    ADD CONSTRAINT instruments_contract_size_positive CHECK (contract_size IS NULL OR contract_size > 0),
    ADD CONSTRAINT instruments_pip_size_positive CHECK (pip_size IS NULL OR pip_size > 0);

COMMENT ON COLUMN instruments.price_scale IS
    'quant.Scale for this instrument''s price-denominated columns (tick_size here; candles.open/high/low/close; latest_quotes.bid/ask). Set at insert; immutable thereafter (see trg_instruments_prevent_scale_update). To correct a wrong scale, delete and re-insert the instrument.';
COMMENT ON COLUMN instruments.qty_scale IS
    'quant.Scale for this instrument''s quantity-denominated columns (qty_step_size, contract_size here; candles.volume). Set at insert; immutable thereafter, same as price_scale.';
COMMENT ON COLUMN instruments.tick_size IS
    'Minimum price increment, scaled by price_scale (quant.Price). Replaces the deprecated NUMERIC tick_size_deprecated.';
COMMENT ON COLUMN instruments.qty_step_size IS
    'Minimum quantity increment, scaled by qty_scale (quant.Qty).';
COMMENT ON COLUMN instruments.contract_size IS
    'Units per contract where the instrument trades in contracts, scaled by qty_scale. NULL where not applicable (ADR-012).';
COMMENT ON COLUMN instruments.pip_size IS
    'Pip size for forex pairs, scaled by price_scale. NULL for non-forex instruments (ADR-012).';

-- Scale is identity, not a mutable attribute (approved decision): it is set
-- once at insert and never changes. This is enforced structurally here,
-- not as a service-layer "reject if candles exist" check -- that check has
-- a race between reading candle existence and writing the update, and
-- guide section 8 calls for concurrency-critical invariants to live in the
-- database. There is no "once candles exist" nuance: no UPDATE may ever
-- touch these two columns, from the moment the row is inserted.
CREATE OR REPLACE FUNCTION instruments_prevent_scale_update() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.price_scale IS DISTINCT FROM OLD.price_scale
       OR NEW.qty_scale IS DISTINCT FROM OLD.qty_scale THEN
        RAISE EXCEPTION
            'instruments.price_scale and instruments.qty_scale are immutable (instrument id=%): delete and re-insert the instrument instead of updating its scale',
            OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_instruments_prevent_scale_update
    BEFORE UPDATE ON instruments
    FOR EACH ROW
    EXECUTE FUNCTION instruments_prevent_scale_update();
