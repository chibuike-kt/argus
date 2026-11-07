-- name: InsertUser :one
INSERT INTO users (email, email_normalized, password_hash)
VALUES ($1, $2, $3)
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
