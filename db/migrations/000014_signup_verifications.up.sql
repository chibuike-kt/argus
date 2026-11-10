-- 000014_signup_verifications: the signup-by-emailed-code flow's own
-- intermediate state (ADR-015 "Signup and onboarding"). No user row
-- exists yet at this point ("no half accounts") -- this table is the
-- only record of an in-progress signup, keyed by email_normalized
-- rather than a user id.
--
-- One row per email, reused across a resend/restart (the ADR: "a new
-- code ... replaces the previous one") via an upsert on the unique
-- email_normalized index, rather than a growing history of attempts --
-- there is exactly one live code/ticket per email at a time by
-- construction.
--
-- code_hash is an HMAC-SHA256 (server key), not a plain SHA-256 like
-- email_tokens/sessions: the code is only 6 digits, low enough entropy
-- that a plain hash would let a database breach be brute-forced
-- offline in an instant; the HMAC key never leaves the server, so a
-- breach of this table alone is not enough.
--
-- ticket_hash is a plain SHA-256 of 32 random bytes, the same shape
-- email_tokens.token_hash already takes -- that token is high entropy,
-- so the same offline-brute-force concern does not apply to it.
--
-- terms_accepted_at/terms_version are captured here at signup start
-- (ADR-015: "Signup start requires accepted_terms: true") and copied
-- onto the users row at signup complete, since the user row the ADR's
-- own fields belong to does not exist until that step.
CREATE TABLE signup_verifications (
    id                 BIGSERIAL   PRIMARY KEY,
    -- email is stored exactly as entered at signup start, the same
    -- "store the real thing, index a canonical form" split the users
    -- table (000010) already uses -- it is what the user row's own
    -- (non-normalized) email column is populated from at signup
    -- complete, since the code was mailed to this exact address.
    email              TEXT        NOT NULL,
    email_normalized   TEXT        NOT NULL UNIQUE,
    code_hash          TEXT        NOT NULL,
    code_sent_at       TIMESTAMPTZ NOT NULL,
    code_expires_at    TIMESTAMPTZ NOT NULL,
    wrong_guesses      INTEGER     NOT NULL DEFAULT 0,
    verified_at        TIMESTAMPTZ,
    ticket_hash        TEXT        UNIQUE,
    ticket_expires_at  TIMESTAMPTZ,
    ticket_used_at     TIMESTAMPTZ,
    terms_accepted_at  TIMESTAMPTZ NOT NULL,
    terms_version      TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
