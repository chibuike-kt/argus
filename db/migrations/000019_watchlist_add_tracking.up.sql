-- 000019_watchlist_add_tracking: Phase 4.1a of the product overhaul brief
-- (decision D15, recorded 2026-11-22). Expand only (ADR-020): two new
-- tables, nothing existing changes.
--
-- instrument_enablements is the audit record of D15.1: adding a Hyperliquid
-- perpetual to a watchlist may enable its instrument, only outside
-- production, through one explicit, audited operation -- who, which
-- instrument, when, and why. One row per enablement, never updated or
-- deleted by the application. user_id is SET NULL when the account is
-- deleted (ADR-016): the record that the instrument was enabled stays, the
-- person does not.
CREATE TABLE instrument_enablements (
    id            BIGSERIAL   PRIMARY KEY,
    instrument_id BIGINT      NOT NULL REFERENCES instruments(id),
    user_id       BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    reason        TEXT        NOT NULL CHECK (reason <> ''),
    environment   TEXT        NOT NULL CHECK (environment <> ''),
    enabled_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_instrument_enablements_instrument ON instrument_enablements (instrument_id, enabled_at);

-- tracked_symbol_states is the worker supervisor's per symbol pipeline
-- state (D15.2, D15.3), read by the API for the watchlist row: a mutable
-- projection, one row per instrument, written in place (like
-- processing_cursors). needs_attention is D15.5 (price precision changed:
-- ingestion stopped, nothing rescaled); stopped is a pipeline the
-- supervisor ended after the removal grace period.
CREATE TABLE tracked_symbol_states (
    instrument_id BIGINT      PRIMARY KEY REFERENCES instruments(id),
    state         TEXT        NOT NULL CHECK (state IN ('waiting', 'loading_history', 'live', 'retrying', 'failed', 'needs_attention', 'stopped')),
    reason        TEXT        NOT NULL DEFAULT '',
    attempt       INTEGER     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The tracked set asks, per instrument, whether any watchlist holds it and
-- since when (min(created_at), the cap order): one index lookup each,
-- instead of scanning every row of a popular instrument (80 ms at 50,000
-- watchlist rows without it, measured).
CREATE INDEX idx_watchlist_items_instrument_created ON watchlist_items (instrument_id, created_at);
