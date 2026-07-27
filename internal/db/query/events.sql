-- name: ListEvents :many
SELECT * FROM events
ORDER BY event_date ASC
LIMIT $1 OFFSET $2;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = $1;

-- name: ListEventsByCounty :many
SELECT * FROM events
WHERE county = $1
ORDER BY event_date ASC
LIMIT $2 OFFSET $3;

-- name: ListEventsByType :many
SELECT * FROM events
WHERE type = $1
ORDER BY event_date ASC
LIMIT $2 OFFSET $3;
