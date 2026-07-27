-- name: ListEvents :many
SELECT * FROM events
ORDER BY event_date ASC
LIMIT $1;

-- name: ListEventsAfter :many
SELECT * FROM events
WHERE event_date > $1 OR (event_date = $1 AND id > $2)
ORDER BY event_date ASC, id ASC
LIMIT $3;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = $1;

-- name: ListEventsByCounty :many
SELECT * FROM events
WHERE county = $1
ORDER BY event_date ASC
LIMIT $2;
