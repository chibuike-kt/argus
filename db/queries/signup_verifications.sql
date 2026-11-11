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

-- IncrementSignupWrongGuessesIfEligible atomically claims one guess
-- against email_normalized's current code: it increments wrong_guesses
-- and returns the row in the same statement, but only if this attempt
-- is still eligible (not already verified, not expired, strictly under
-- max_wrong_guesses) BEFORE the increment -- one UPDATE, not a
-- separate read-then-increment pair (architect review of Part 2, item
-- 3): N concurrent requests reading the same pre-increment count and
-- all getting a guess is exactly the race this replaces. Zero rows
-- back means this attempt is rejected outright, before the code is
-- even compared -- the caller must not decrement or otherwise "give
-- back" the guess.
-- name: IncrementSignupWrongGuessesIfEligible :one
UPDATE signup_verifications
SET wrong_guesses = wrong_guesses + 1
WHERE email_normalized = $1 AND verified_at IS NULL
    AND code_expires_at > $2 AND wrong_guesses < $3
RETURNING *;

-- MarkSignupVerified issues a ticket and atomically transitions the
-- row to verified, but only if it is still eligible (not already
-- verified, not expired) -- one UPDATE, not a separate check-then-mark
-- pair, so two concurrent correct submissions of the same code can
-- never both receive a valid ticket (the same single-use-token race
-- ConsumeEmailToken's own comment describes). Zero rows back means the
-- caller must treat this as a failed verification, even though the
-- code it already checked (in Go, before calling this) was correct.
-- Does NOT re-check wrong_guesses (unlike an earlier version of this
-- query): IncrementSignupWrongGuessesIfEligible above already proved,
-- atomically, that the one attempt now being completed was allowed
-- before its own increment -- re-checking the post-increment count
-- here would let a legitimate, correctly-guessed final attempt (e.g.
-- the 5th, exactly at the limit) fail only because of its own guess
-- being counted.
-- name: MarkSignupVerified :one
UPDATE signup_verifications
SET verified_at = $2, ticket_hash = $3, ticket_expires_at = $4
WHERE email_normalized = $1 AND verified_at IS NULL AND code_expires_at > $5
RETURNING *;

-- GetSignupVerificationByTicket is a read-only lookup by ticket hash
-- -- it does not consume the ticket (architect review of Part 2, item
-- 2): SignupComplete needs the row's email to validate the password
-- policy, and must be able to do that before committing to consuming
-- the ticket, so a rejected password never burns it.
-- name: GetSignupVerificationByTicket :one
SELECT * FROM signup_verifications WHERE ticket_hash = $1;

-- ConsumeSignupTicket marks a ticket used and returns the row,
-- atomically, only if it exists, has not been used yet, and has not
-- expired -- the same single-use shape as ConsumeEmailToken.
-- name: ConsumeSignupTicket :one
UPDATE signup_verifications
SET ticket_used_at = $2
WHERE ticket_hash = $1 AND ticket_used_at IS NULL AND ticket_expires_at > $3
RETURNING *;
