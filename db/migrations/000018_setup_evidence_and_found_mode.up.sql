-- 000018_setup_evidence_and_found_mode: Phase 6.1 of the product overhaul
-- brief (D6, owner decisions of 2026-11-19). Expand only (ADR-020): two
-- additive columns, no rewrite of any existing row's values.
--
-- setups.found_mode: whether the setup was found live (the candle that
-- created it was the newest closed candle when the worker processed it)
-- or in replay (the worker was still catching up on older candles). Set
-- once, at creation, never changed. Every existing row is 'unknown': it
-- was never recorded, and it is not guessed from timing afterwards.
ALTER TABLE setups
    ADD COLUMN found_mode TEXT NOT NULL DEFAULT 'unknown'
    CONSTRAINT setups_found_mode_check CHECK (found_mode IN ('live', 'replay', 'unknown'));

-- setup_events.evidence: the evidence chain the engine used at the
-- decision (D6, an ADR-011 amendment): the ordered steps of the story
-- (sweep, break, premium or discount, structural liquidity, POI, plan),
-- each with its candle open times and prices, the spec section it came
-- from and its result. Written on the detected event and on amendment
-- events of new setups. The JSON value null means "not recorded": every
-- event written before this migration, and every event type that carries
-- none. (A JSON null in a NOT NULL column rather than SQL NULL keeps the
-- generated Go type the plain json.RawMessage plan_snapshot already uses,
-- with no new dependency; a constant default does not rewrite the table.)
-- The row stays immutable (trg_setup_events_prevent_update is unchanged).
ALTER TABLE setup_events
    ADD COLUMN evidence JSONB NOT NULL DEFAULT 'null'::jsonb;
