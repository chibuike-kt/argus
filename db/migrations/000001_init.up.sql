-- 000001_init: canonical instrument registry (ADR-006).
-- Instruments are provider-qualified. A symbol existing at a provider does not
-- imply enablement; enablement is an explicit state gated on data rights and
-- historical coverage (ADR-006, ADR-007).

CREATE TABLE IF NOT EXISTS instruments (
    id                 BIGSERIAL PRIMARY KEY,
    provider           TEXT        NOT NULL,
    native_symbol      TEXT        NOT NULL,
    asset_class        TEXT        NOT NULL,
    base_asset         TEXT,
    quote_asset        TEXT,
    contract_type      TEXT,
    price_precision     INTEGER     NOT NULL DEFAULT 0,
    quantity_precision  INTEGER     NOT NULL DEFAULT 0,
    tick_size          NUMERIC,
    supported_intervals TEXT[]     NOT NULL DEFAULT '{}',
    enabled            BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, native_symbol)
);

CREATE INDEX IF NOT EXISTS idx_instruments_enabled ON instruments (enabled) WHERE enabled = TRUE;
