-- name: GetUserPreferences :one
SELECT * FROM user_preferences WHERE user_id = $1;

-- InsertUserPreferences creates the first version. ON CONFLICT DO NOTHING:
-- a row that already exists means the caller's "version 0" was stale, and
-- zero rows returned is exactly how the store reports that (409).
-- name: InsertUserPreferences :one
INSERT INTO user_preferences (user_id, data, version, updated_at)
VALUES ($1, $2, 1, $3)
ON CONFLICT (user_id) DO NOTHING
RETURNING *;

-- UpdateUserPreferences writes only when the stored version is the one
-- the caller based its change on; zero rows means stale (409).
-- name: UpdateUserPreferences :one
UPDATE user_preferences
SET data = $2, version = version + 1, updated_at = $4
WHERE user_id = $1 AND version = $3
RETURNING *;

-- name: GetUserDrawings :one
SELECT * FROM user_drawings WHERE user_id = $1 AND instrument_id = $2 AND timeframe = $3;

-- name: InsertUserDrawings :one
INSERT INTO user_drawings (user_id, instrument_id, timeframe, data, version, updated_at)
VALUES ($1, $2, $3, $4, 1, $5)
ON CONFLICT (user_id, instrument_id, timeframe) DO NOTHING
RETURNING *;

-- name: UpdateUserDrawings :one
UPDATE user_drawings
SET data = $4, version = version + 1, updated_at = $6
WHERE user_id = $1 AND instrument_id = $2 AND timeframe = $3 AND version = $5
RETURNING *;
