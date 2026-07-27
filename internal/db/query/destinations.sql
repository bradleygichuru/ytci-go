-- name: ListDestinations :many
SELECT * FROM destinations
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetDestinationBySlug :one
SELECT * FROM destinations WHERE slug = $1 LIMIT 1;

-- name: GetDestinationByID :one
SELECT * FROM destinations WHERE id = $1 LIMIT 1;

-- name: CreateDestination :one
INSERT INTO destinations (
    name, slug, county, locality, category, status,
    location, map_label, access_route, distance_reference,
    short_description, full_description, significance, history,
    things_to_do, suitable_audiences, duration, difficulty, seasonality,
    indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes,
    source, content_owner, verification_status, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6,
    ST_SetSRID(ST_MakePoint($7, $8), 4326), $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26,
    $27, $28, $29, $30
) RETURNING *;

-- name: UpdateDestination :one
UPDATE destinations SET
    name = COALESCE($2, name),
    short_description = COALESCE($3, short_description),
    status = COALESCE($4, status),
    updated_at = now()
WHERE id = $1 RETURNING *;

-- name: FindNearbyDestinations :many
SELECT *, ST_Distance(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) AS distance_meters
FROM destinations
WHERE location IS NOT NULL
  AND status = 'published'
  AND ST_DWithin(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
ORDER BY distance_meters
LIMIT $4;
