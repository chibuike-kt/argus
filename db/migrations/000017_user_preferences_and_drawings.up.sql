-- 000017_user_preferences_and_drawings: per-user, server-side storage
-- for everything a user sets (owner decision D1, approved 2026-11-17,
-- brief-argus-product-overhaul.md Phase 1): so a choice survives a
-- refresh, a cleared browser and a login on another device. The browser
-- keeps a localStorage copy only as a fast cache.
--
-- Expand-only (ADR-020): two new tables, nothing existing changes.
--
-- version is an optimistic-concurrency counter: a write names the
-- version it was based on and is refused (409) when that is stale, so
-- two tabs or two devices never silently overwrite each other.
--
-- Ownership is user_id alone, taken from the session server-side, never
-- from the client (ADR-015). ON DELETE CASCADE: the rows go with the
-- account (ADR-016). Document size and shape are validated in Go
-- (internal/preferences) -- this project's convention of validating in
-- one visible place -- with only the JSON type checked here.
CREATE TABLE user_preferences (
    user_id     BIGINT      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    data        JSONB       NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    version     INTEGER     NOT NULL CHECK (version >= 1),
    updated_at  TIMESTAMPTZ NOT NULL
);

-- One drawings document per (user, instrument, timeframe): the drawings
-- a user placed on that chart, as a JSON array of drawing objects.
CREATE TABLE user_drawings (
    user_id        BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    instrument_id  BIGINT      NOT NULL REFERENCES instruments(id),
    timeframe      TEXT        NOT NULL,
    data           JSONB       NOT NULL CHECK (jsonb_typeof(data) = 'array'),
    version        INTEGER     NOT NULL CHECK (version >= 1),
    updated_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, instrument_id, timeframe)
);
