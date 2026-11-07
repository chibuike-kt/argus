-- 000011_auth_sessions: opaque server-side sessions (ADR-015 addendum
-- row 5). Postgres is the source of truth -- a session is valid if and
-- only if a row here says so; Redis is never consulted to validate one
-- (the addendum's own point: a Redis outage must not silently either
-- log everyone out or leave revoked sessions still accepted).
--
-- token_hash, not the token itself: the raw 32-byte token only ever
-- exists in the Set-Cookie response and the client's own cookie jar --
-- this table (and anything that can read it, including a backup) only
-- ever holds something that cannot be replayed as a session token on
-- its own, the same reason email_tokens (000012) hashes its tokens too.
--
-- last_password_at seeds sensitive-action reauth (addendum row 5's
-- "require the password again when the last password entry on that
-- session is older than 10 minutes"): a fresh login is itself a
-- password entry, so it is set alongside created_at, then advanced only
-- when the user re-enters their password (Part 2's reauth endpoint).
CREATE TABLE sessions (
    id                   BIGSERIAL   PRIMARY KEY,
    user_id              BIGINT      NOT NULL REFERENCES users(id),
    token_hash           TEXT        NOT NULL UNIQUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    idle_expires_at      TIMESTAMPTZ NOT NULL,
    absolute_expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,
    user_agent           TEXT,
    ip                   TEXT,
    last_password_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);
-- token_hash's own UNIQUE constraint already creates the index a
-- lookup-by-token needs; no second index on it.
