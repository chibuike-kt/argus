-- db/seed/dev_fixture_instruments.sql
--
-- DEV/TEST TOOLING ONLY. NEVER RUN THIS AGAINST A PRODUCTION DATABASE.
--
-- This does not resolve ADR-007 (provider selection and commercial
-- data rights) and adds no code path to internal/instruments that can
-- set enabled = TRUE -- InsertInstrument there still always writes
-- enabled = FALSE, deliberately, until that ADR resolves. This file
-- sets enabled = TRUE directly via SQL, bypassing instruments.Store
-- entirely, because it is tooling for a developer's own local
-- database: it seeds two fixture-backed instruments so
-- cmd/argus-worker's dev-fixture-ingest mode (gated by
-- ARGUS_DEV_FIXTURE_INGEST, see cmd/argus-worker/main.go) has
-- something to process. It is not a migration (db/migrations is the
-- production sequence, applied by `make migrate-up`) -- apply this
-- one explicitly with `make seed-dev` against whatever
-- ARGUS_DATABASE_URL is currently set to: your own local Postgres,
-- never a shared or production one.
--
-- provider = 'fixture' must match the providerName
-- cmd/argus-worker/main.go passes to ingestion.NewService in
-- dev-fixture-ingest mode, and each native_symbol must match a symbol
-- key in internal/ingestion/fixture/testdata/dev_dataset.json -- both
-- sides of that match are what let ingestion.InstrumentCache resolve
-- these rows at ingest time. Idempotent: safe to re-run.
INSERT INTO instruments (
    provider, native_symbol, asset_class, base_asset, quote_asset,
    contract_type, price_scale, qty_scale, tick_size, qty_step_size,
    supported_intervals, enabled
) VALUES
    ('fixture', 'BTC-PERP', 'crypto_perp', 'BTC', 'USD', 'perpetual', 0, 0, 1, 1, ARRAY['1m'], TRUE),
    ('fixture', 'ETH-PERP', 'crypto_perp', 'ETH', 'USD', 'perpetual', 0, 0, 1, 1, ARRAY['1m'], TRUE)
ON CONFLICT (provider, native_symbol) DO UPDATE SET enabled = TRUE;
