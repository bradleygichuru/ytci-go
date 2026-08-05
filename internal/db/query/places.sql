-- name: UpsertGooglePlacesCache :exec
INSERT INTO google_places_cache (place_id, name, formatted_address, lat, lng, types, data, cached_at, hero_image_url, hero_image_source, hero_image_attribution)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8, $9, $10)
ON CONFLICT (place_id) DO UPDATE SET
    name = EXCLUDED.name,
    formatted_address = EXCLUDED.formatted_address,
    lat = EXCLUDED.lat,
    lng = EXCLUDED.lng,
    types = EXCLUDED.types,
    data = EXCLUDED.data,
    cached_at = now(),
    hero_image_url = EXCLUDED.hero_image_url,
    hero_image_source = EXCLUDED.hero_image_source,
    hero_image_attribution = EXCLUDED.hero_image_attribution;

-- name: GetGooglePlacesCache :one
SELECT * FROM google_places_cache WHERE place_id = $1;

-- name: GetGooglePlacesCacheFresh :one
SELECT * FROM google_places_cache
WHERE place_id = $1 AND cached_at > now() - interval '30 days';

-- name: UpsertGooglePlacesSearchCache :exec
INSERT INTO google_places_search_cache (query_hash, response, cached_at, expires_at)
VALUES ($1, $2, now(), $3)
ON CONFLICT (query_hash) DO UPDATE SET
    response = EXCLUDED.response,
    cached_at = now(),
    expires_at = EXCLUDED.expires_at;

-- name: GetGooglePlacesSearchCache :one
SELECT * FROM google_places_search_cache
WHERE query_hash = $1 AND expires_at > now();

-- name: GetGooglePlacesSearchCacheStale :one
SELECT * FROM google_places_search_cache
WHERE query_hash = $1 AND cached_at > now() - interval '7 days';

-- name: CreateGooglePlacesDraft :one
INSERT INTO destinations (name, slug, county, locality, category, status, source, google_place_id, location, short_description)
VALUES ($1, $2, $3, $4, $5, 'draft', 'google_places', $6, ST_SetSRID(ST_MakePoint($7, $8), 4326), $9)
RETURNING *;

-- name: GetDestinationByGooglePlaceID :one
SELECT * FROM destinations
WHERE google_place_id = $1
LIMIT 1;

-- name: GetPopularDestinations :many
SELECT d.*, COUNT(bli.id) AS save_count
FROM destinations d
LEFT JOIN bucket_list_items bli ON bli.destination_id = d.id
WHERE d.status = 'published'
GROUP BY d.id
ORDER BY save_count DESC, d.updated_at DESC
LIMIT $1;

-- name: ListPublishedDestinationsWithoutPlaceID :many
SELECT id, name, slug, county, locality, category, status, location,
       map_label, access_route, distance_reference,
       short_description, full_description, significance, history,
       things_to_do, suitable_audiences, duration, difficulty, seasonality,
       indicative_fees, opening_info, transport_notes, accessibility, facilities, safety_notes,
       source, content_owner, verification_status,
       last_updated, review_date, created_at, updated_at, google_place_id
FROM destinations
WHERE status = 'published' AND google_place_id IS NULL
ORDER BY name;

-- name: UpdateDestinationPlaceID :exec
UPDATE destinations SET google_place_id = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateGooglePlacesHeroImage :exec
INSERT INTO google_places_cache (place_id, hero_image_url, hero_image_source, hero_image_attribution)
VALUES ($1, $2, $3, $4)
ON CONFLICT (place_id) DO UPDATE SET
    hero_image_url = EXCLUDED.hero_image_url,
    hero_image_source = EXCLUDED.hero_image_source,
    hero_image_attribution = EXCLUDED.hero_image_attribution;

-- name: GetGooglePlacesHeroImage :one
SELECT hero_image_url, hero_image_source, hero_image_attribution
FROM google_places_cache
WHERE place_id = $1;
