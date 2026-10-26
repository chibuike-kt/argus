DROP TRIGGER IF EXISTS trg_instruments_prevent_scale_update ON instruments;
DROP FUNCTION IF EXISTS instruments_prevent_scale_update();

ALTER TABLE instruments
    DROP CONSTRAINT IF EXISTS instruments_price_scale_range,
    DROP CONSTRAINT IF EXISTS instruments_qty_scale_range,
    DROP CONSTRAINT IF EXISTS instruments_tick_size_nonnegative,
    DROP CONSTRAINT IF EXISTS instruments_qty_step_size_nonnegative,
    DROP CONSTRAINT IF EXISTS instruments_contract_size_positive,
    DROP CONSTRAINT IF EXISTS instruments_pip_size_positive;

ALTER TABLE instruments
    DROP COLUMN price_scale,
    DROP COLUMN qty_scale,
    DROP COLUMN tick_size,
    DROP COLUMN qty_step_size,
    DROP COLUMN contract_size,
    DROP COLUMN pip_size;

ALTER TABLE instruments
    RENAME COLUMN tick_size_deprecated TO tick_size;
