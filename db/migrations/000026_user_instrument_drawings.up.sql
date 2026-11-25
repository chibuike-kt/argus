-- 000026_user_instrument_drawings: one drawings document per (user,
-- instrument), owner decision D17 (a user's drawings belong to an
-- instrument and show on every timeframe; ADR-008: "User drawings must
-- remain anchored consistently when users change chart timeframes").
--
-- Expand only (ADR-020). The per (user, instrument, timeframe) documents of
-- 000017's user_drawings stay exactly as they are in this release: nothing
-- is deleted or rewritten. The application merges them into this table the
-- first time a user reads an instrument's drawings (internal/preferences,
-- GetDrawings), and never writes user_drawings again. A later contract
-- migration drops user_drawings once every release reads only this table.
--
-- Same rules as user_drawings: ownership is user_id alone, taken from the
-- session server side (ADR-015); the rows go with the account (ON DELETE
-- CASCADE, ADR-016); version is the optimistic concurrency counter.
CREATE TABLE user_instrument_drawings (
    user_id        BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    instrument_id  BIGINT      NOT NULL REFERENCES instruments(id),
    data           JSONB       NOT NULL CHECK (jsonb_typeof(data) = 'array'),
    version        INTEGER     NOT NULL CHECK (version >= 1),
    updated_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, instrument_id)
);
