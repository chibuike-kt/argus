-- db/seed/e2e_only_instruments.sql
--
-- E2E TOOLING ONLY -- applied ONLY by Makefile's e2e-stack target,
-- against E2E_DB_URL (argus_e2e), never by `make seed-dev` (which
-- stays dev_fixture_instruments.sql's own, unchanged scope) and NEVER
-- against production. Disabled by default the same way every
-- instrument is (instruments.Store.Insert can never write
-- enabled = TRUE -- ADR-007 is still open); this file sets it TRUE
-- directly via SQL for the e2e database alone, the same bypass
-- dev_fixture_instruments.sql already uses for BTC-PERP/ETH-PERP.
--
-- SOL-PERP: a flat-price fixture symbol
-- (internal/ingestion/fixture/testdata/dev_dataset.json,
-- O=H=L=C=150.00 for every candle) -- mathematically never produces a
-- structural swing (the engine's own LeftBars/RightBars=2 swing
-- detection requires a STRICT local max/min; a constant series has
-- none), so this instrument gets real ingested candles (the chart
-- renders, live updates work) but deterministically zero setups,
-- ever. This is web/e2e/global-setup.ts's own instrument-with-no-
-- setups fixture (analysis-workspace.tsx's real bug this covers: an
-- instrument with no setups used to never resolve at all) -- the
-- real-world shape of the bug, not an instrument with no data at all.
-- Idempotent: safe to re-run.
INSERT INTO instruments (
    provider, native_symbol, asset_class, base_asset, quote_asset,
    contract_type, price_scale, qty_scale, tick_size, qty_step_size,
    supported_intervals, enabled
) VALUES
    ('fixture', 'SOL-PERP', 'crypto_perp', 'SOL', 'USD', 'perpetual', 0, 0, 1, 1, ARRAY['1m','5m','15m','1h','4h','1d'], TRUE)
ON CONFLICT (provider, native_symbol) DO UPDATE SET enabled = TRUE, supported_intervals = EXCLUDED.supported_intervals;
