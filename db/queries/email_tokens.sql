-- name: InsertEmailToken :one
INSERT INTO email_tokens (user_id, purpose, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- ConsumeEmailToken marks a token used and returns the row, atomically,
-- only if it exists for the given purpose, has not been used yet, and
-- has not expired -- one statement, not a separate check-then-mark
-- pair, so two concurrent requests consuming the same token can never
-- both see it as valid (classic single-use-token TOCTOU race). Zero
-- rows back means "invalid, already used, or expired"; the caller
-- cannot and does not need to distinguish which.
-- name: ConsumeEmailToken :one
UPDATE email_tokens SET used_at = $4
WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > $3
RETURNING *;
