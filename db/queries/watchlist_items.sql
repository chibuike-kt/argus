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

-- DeleteWatchlistItem removes one instrument from userID's own watchlist
-- (an item-level remove, so two tabs never clobber each other the way a
-- whole-list replace can). The user_id predicate is the ownership check:
-- a row of another user can never match. The remaining rows keep their
-- positions; a gap is harmless because the list is read ORDER BY
-- position. Zero rows affected means it was not on the list (idempotent).
-- name: DeleteWatchlistItem :execrows
DELETE FROM watchlist_items WHERE user_id = $1 AND instrument_id = $2;
