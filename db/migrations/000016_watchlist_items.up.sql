-- 000016_watchlist_items: each user's own watchlist of enabled
-- instruments with an order (ADR-015 "Signup and onboarding": "a
-- watchlist of enabled instruments with an order (maximum 50)";
-- ADR-016: "Watchlists are private"). The 50-item cap and the
-- "instrument must be enabled" check are both enforced in Go
-- (internal/watchlist) at write time, not here -- consistent with this
-- project's own convention of validating in one visible place rather
-- than a DB CHECK/trigger.
--
-- position is the user's own display order, set by PUT /watchlist
-- replacing the whole list at once (simplest shape for a single
-- settings/onboarding write, not an incremental add/remove/reorder
-- API) -- 0-based, dense, no gaps, by construction of that replace.
CREATE TABLE watchlist_items (
    id             BIGSERIAL   PRIMARY KEY,
    user_id        BIGINT      NOT NULL REFERENCES users(id),
    instrument_id  BIGINT      NOT NULL REFERENCES instruments(id),
    position       INTEGER     NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, instrument_id)
);

CREATE INDEX idx_watchlist_items_user_id ON watchlist_items (user_id);
