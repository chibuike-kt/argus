-- InsertVerifiedUser is the only way a users row is ever created
-- (ADR-015: "no half accounts" -- signup complete is the one call
-- site, after the emailed code and the password step). The row is
-- created already verified, with terms_accepted_at/terms_version
-- carried over from the signup_verifications row that is consumed
-- alongside it.
-- name: InsertVerifiedUser :one
INSERT INTO users (
    email, email_normalized, password_hash, email_verified_at,
    terms_accepted_at, terms_version
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByEmailNormalized :one
SELECT * FROM users WHERE email_normalized = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: MarkEmailVerified :one
UPDATE users SET email_verified_at = $2, updated_at = $2
WHERE id = $1
RETURNING *;

-- UpdatePasswordHash is the rehash-on-login path (ADR-015 addendum row
-- 2): login already has the plaintext password in hand (it just
-- verified it against the stored hash), so a parameter change is
-- applied here rather than waiting for a password-change flow no one
-- may ever trigger.
-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = $2, updated_at = $3
WHERE id = $1;

-- name: SetDisplayName :one
UPDATE users SET display_name = $2, updated_at = $3
WHERE id = $1
RETURNING *;

-- MarkOnboardingCompleted is unconditional -- the service layer
-- (not this statement) is what checks display_name and at least one
-- watchlist item already exist before calling it, so the check stays
-- in one visible, testable place in Go rather than a SQL WHERE clause
-- duplicating it.
-- name: MarkOnboardingCompleted :one
UPDATE users SET onboarding_completed_at = $2, updated_at = $2
WHERE id = $1
RETURNING *;
