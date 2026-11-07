-- 000012_auth_email_tokens: single-use email tokens (ADR-015 addendum
-- row 12). One table for both purposes this project needs (email
-- verification now, password reset in Part 2) rather than two near-
-- identical tables -- they share every column; only the CHECK
-- constraint and the expiry duration (chosen in Go, by purpose) differ.
--
-- token_hash, not the token itself -- the same reason sessions (000011)
-- hashes its own token.
CREATE TABLE email_tokens (
    id          BIGSERIAL   PRIMARY KEY,
    user_id     BIGINT      NOT NULL REFERENCES users(id),
    purpose     TEXT        NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    token_hash  TEXT        NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ
);

CREATE INDEX idx_email_tokens_user_id ON email_tokens (user_id);
