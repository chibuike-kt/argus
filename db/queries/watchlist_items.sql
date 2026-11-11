-- name: ListWatchlistItems :many
SELECT * FROM watchlist_items WHERE user_id = $1 ORDER BY position ASC;

-- name: CountWatchlistItems :one
SELECT count(*) FROM watchlist_items WHERE user_id = $1;

-- name: DeleteWatchlistItems :exec
DELETE FROM watchlist_items WHERE user_id = $1;

-- name: InsertWatchlistItem :one
INSERT INTO watchlist_items (user_id, instrument_id, position)
VALUES ($1, $2, $3)
RETURNING *;
