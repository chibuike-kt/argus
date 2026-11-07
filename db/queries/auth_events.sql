-- name: InsertAuthEvent :exec
INSERT INTO auth_events (at, user_id, kind, ip, user_agent, detail)
VALUES ($1, $2, $3, $4, $5, $6);

-- ListAuthEventsForUser exists for tests (confirming an event was
-- written with the right shape) -- nothing in the product itself reads
-- this table back yet.
-- name: ListAuthEventsForUser :many
SELECT * FROM auth_events WHERE user_id = $1 ORDER BY id;
