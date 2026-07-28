-- name: GetMobileDestinationBySlug :one
SELECT id, name, slug, county, locality, category,
       short_description, full_description, significance, history,
       things_to_do, suitable_audiences, duration, difficulty, seasonality,
       indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes,
       map_label, access_route, distance_reference,
       ST_X(location::geometry) AS lng, ST_Y(location::geometry) AS lat,
       created_at, updated_at
FROM destinations
WHERE slug = $1 AND status = 'published'
LIMIT 1;

-- name: ListMobileDestinations :many
SELECT id, name, slug, county, locality, category,
       short_description, full_description, significance, history,
       things_to_do, suitable_audiences, duration, difficulty, seasonality,
       indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes,
       map_label, access_route, distance_reference,
       ST_X(location::geometry) AS lng, ST_Y(location::geometry) AS lat,
       created_at, updated_at
FROM destinations
WHERE status = 'published'
ORDER BY created_at DESC
LIMIT $1;

-- name: ListActiveChallenges :many
SELECT id, title, description, badge_name, badge_icon_url,
       start_date, end_date, status, created_at
FROM challenges
WHERE status = 'active'
ORDER BY end_date ASC
LIMIT $1;

-- name: ListPublishedCourses :many
SELECT id, title, description, difficulty, image_url, created_at
FROM courses
WHERE status = 'published'
ORDER BY created_at DESC
LIMIT $1;

-- name: ListScheduledEvents :many
SELECT id, title, organizer, county, venue, event_date, type,
       description, contact_email, contact_phone, image_url, created_at
FROM events
WHERE status = 'scheduled'
ORDER BY event_date ASC
LIMIT $1;

-- name: ListPublicConservation :many
SELECT id, title, organizer, description, event_date,
       impact_metric, impact_target, current_participants, participant_limit,
       status, location_label,
       ST_X(location::geometry) AS lng, ST_Y(location::geometry) AS lat,
       created_at
FROM conservation_activities
WHERE privacy_level = 'public' AND status = 'open'
ORDER BY created_at DESC
LIMIT $1;

-- name: FindNearbyMobileDestinations :many
SELECT id, name, slug, county, locality, category,
       short_description, full_description, significance, history,
       things_to_do, suitable_audiences, duration, difficulty, seasonality,
       indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes,
       map_label, access_route, distance_reference,
       ST_X(location::geometry) AS lng, ST_Y(location::geometry) AS lat,
       ST_Distance(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) AS distance_meters,
       created_at, updated_at
FROM destinations
WHERE location IS NOT NULL
  AND status = 'published'
  AND ST_DWithin(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
ORDER BY distance_meters
LIMIT $4;
