-- name: InsertSession :one
INSERT INTO sessions (
    user_id, token_hash, created_at, last_seen_at,
    idle_expires_at, absolute_expires_at, user_agent, ip, last_password_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- GetSessionByTokenHash returns the raw row regardless of revoked/idle/
-- absolute expiry state -- session.Resolve (internal/identity/session)
-- decides what "valid" means in Go, in one place, rather than that
-- logic being split between a WHERE clause here and a second check
-- there.
-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = $1;

-- TouchSession slides the idle expiry and records last_seen_at -- the
-- caller (the auth middleware) throttles how often this actually runs
-- (at most once a minute per session, ADR-015's own instruction), not
-- this query.
-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2, idle_expires_at = $3
WHERE id = $1;

-- GetSessionByIDForUser is DELETE /auth/sessions/{id}'s own existence +
-- ownership check, before revoking: sql.ErrNoRows here means exactly
-- "404" whether the id does not exist at all or belongs to a different
-- user (the brief's own instruction -- a session id that is not yours
-- should not even confirm that it exists, so both cases produce the
-- identical error).
-- name: GetSessionByIDForUser :one
SELECT * FROM sessions WHERE id = $1 AND user_id = $2;

-- RevokeSessionForUser is both "logout" (the session's own id, its own
-- user_id) and "DELETE /auth/sessions/{id}" (an id the caller must
-- also own, checked separately above before this runs) in one query:
-- the user_id = $2 guard is defense in depth, and revoked_at IS NULL
-- makes a second revoke of an already-revoked session affect zero rows
-- rather than erroring -- logout's own required idempotence, which
-- DELETE /auth/sessions/{id} gets for free by sharing this query.
-- name: RevokeSessionForUser :execrows
UPDATE sessions SET revoked_at = $3
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :exec
UPDATE sessions SET revoked_at = $2
WHERE user_id = $1 AND revoked_at IS NULL;

-- ListActiveSessionsForUser is GET /auth/sessions' own read path:
-- neither revoked nor expired (idle or absolute) as of now.
-- name: ListActiveSessionsForUser :many
SELECT * FROM sessions
WHERE user_id = $1 AND revoked_at IS NULL AND idle_expires_at > $2 AND absolute_expires_at > $2
ORDER BY last_seen_at DESC;

-- UpdateLastPasswordAt is Part 2's own reauth path (ADR-015 addendum
-- row 5) -- included now so the column has a writer once that work
-- lands; nothing in Part 1 calls it.
-- name: UpdateLastPasswordAt :exec
UPDATE sessions SET last_password_at = $2
WHERE id = $1;
