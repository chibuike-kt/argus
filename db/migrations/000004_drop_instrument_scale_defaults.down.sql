ALTER TABLE instruments
    ALTER COLUMN price_scale SET DEFAULT 0,
    ALTER COLUMN qty_scale SET DEFAULT 0,
    ALTER COLUMN tick_size SET DEFAULT 0,
    ALTER COLUMN qty_step_size SET DEFAULT 0;
