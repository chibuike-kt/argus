-- name: GetSignupVerificationByEmail :one
SELECT * FROM signup_verifications WHERE email_normalized = $1;

-- UpsertSignupCode starts or restarts a signup: a fresh code, wrong
-- guesses reset, and any previous verification/ticket cleared, since
-- every signup/start call issues a brand new code that replaces
-- whatever was live before (ADR-015: "a new code ... replaces the
-- previous one"). terms_accepted_at/terms_version are overwritten too
-- -- accepted_terms is required on every signup/start call, not only
-- the first.
-- name: UpsertSignupCode :one
INSERT INTO signup_verifications (
    email, email_normalized, code_hash, code_sent_at, code_expires_at,
    terms_accepted_at, terms_version
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (email_normalized) DO UPDATE SET
    email = EXCLUDED.email,
    code_hash = EXCLUDED.code_hash,
    code_sent_at = EXCLUDED.code_sent_at,
    code_expires_at = EXCLUDED.code_expires_at,
    wrong_guesses = 0,
    verified_at = NULL,
    ticket_hash = NULL,
    ticket_expires_at = NULL,
    ticket_used_at = NULL,
    terms_accepted_at = EXCLUDED.terms_accepted_at,
    terms_version = EXCLUDED.terms_version
RETURNING *;

-- name: IncrementSignupWrongGuesses :exec
UPDATE signup_verifications SET wrong_guesses = wrong_guesses + 1
WHERE email_normalized = $1;

-- MarkSignupVerified issues a ticket and atomically transitions the
-- row to verified, but only if it is still eligible (not already
-- verified, not expired, not exhausted) -- one UPDATE, not a separate
-- check-then-mark pair, so two concurrent correct submissions of the
-- same code can never both receive a valid ticket (the same single-
-- use-token race ConsumeEmailToken's own comment describes). Zero
-- rows back means the caller must treat this as a failed verification,
-- even though the code it already checked (in Go, before calling this)
-- was correct.
-- name: MarkSignupVerified :one
UPDATE signup_verifications
SET verified_at = $2, ticket_hash = $3, ticket_expires_at = $4
WHERE email_normalized = $1 AND verified_at IS NULL
    AND wrong_guesses < $5 AND code_expires_at > $6
RETURNING *;

-- ConsumeSignupTicket marks a ticket used and returns the row,
-- atomically, only if it exists, has not been used yet, and has not
-- expired -- the same single-use shape as ConsumeEmailToken.
-- name: ConsumeSignupTicket :one
UPDATE signup_verifications
SET ticket_used_at = $2
WHERE ticket_hash = $1 AND ticket_used_at IS NULL AND ticket_expires_at > $3
RETURNING *;
