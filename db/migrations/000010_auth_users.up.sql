-- 000010_auth_users: account identity (ADR-015). The first of four
-- authentication migrations (users, sessions, email_tokens,
-- auth_events) -- split one table per file, like 000009, since each
-- stands on its own and a later migration needing to touch only one of
-- them should not carry the others.
--
-- email is stored exactly as entered (for display and for the email
-- actually sent to); email_normalized (lowercased, trimmed) is the
-- unique, lookup key -- the same "store the real thing, index a
-- canonical form" split this project already uses elsewhere (e.g.
-- native_symbol vs a display symbol). Normalization itself lives in Go
-- (internal/identity), not a generated column, so the exact rule is one
-- visible, testable place, not duplicated in SQL.
--
-- password_hash is a PHC string (encodes the Argon2id parameters
-- alongside the hash and salt, ADR-015 addendum), never split into
-- separate columns -- argon2's own libraries parse/produce this format
-- directly, and a parameter change becomes a data migration problem,
-- not a column migration problem, which is exactly why rehashing on
-- login exists instead.
CREATE TABLE users (
    id                  BIGSERIAL   PRIMARY KEY,
    email               TEXT        NOT NULL,
    email_normalized    TEXT        NOT NULL UNIQUE,
    password_hash       TEXT        NOT NULL,
    email_verified_at   TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at         TIMESTAMPTZ
);
