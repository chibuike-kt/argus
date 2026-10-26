-- 000004_drop_instrument_scale_defaults: 000002 added price_scale,
-- qty_scale, tick_size and qty_step_size with DEFAULT 0, which let an
-- insert that omitted them silently succeed with scale 0 instead of
-- failing (approved decision: scale must be set explicitly, every
-- instrument states its own precision). 000002 is already shipped on
-- main, so this is a follow-up contract-style migration rather than an
-- edit to it (ADR-020 expand-contract; db/migrations is append-only once
-- a migration has shipped).
--
-- Dropping a column default does not touch existing rows: any instrument
-- already inserted keeps whatever value it has. It only removes the
-- fallback for future inserts, which is exactly what should happen --
-- every column is still NOT NULL, so an insert that omits one now fails
-- outright instead of silently defaulting to 0.

ALTER TABLE instruments
    ALTER COLUMN price_scale DROP DEFAULT,
    ALTER COLUMN qty_scale DROP DEFAULT,
    ALTER COLUMN tick_size DROP DEFAULT,
    ALTER COLUMN qty_step_size DROP DEFAULT;
