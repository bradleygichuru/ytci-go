-- Rich itinerary stops for composer suggestions: unverified Google Places
-- stops are stored without a destination_id, with provenance/coordinates and
-- a verification flag so the workspace never presents them as program data.

ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS place_id TEXT;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS provenance TEXT;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS county TEXT;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS verified BOOLEAN;
ALTER TABLE itinerary_stops ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'stop';

ALTER TABLE itinerary_stops DROP CONSTRAINT IF EXISTS itinerary_stops_provenance_check;
ALTER TABLE itinerary_stops
    ADD CONSTRAINT itinerary_stops_provenance_check
    CHECK (provenance IS NULL OR provenance IN ('curated', 'unverified', 'custom'));

ALTER TABLE itinerary_stops DROP CONSTRAINT IF EXISTS itinerary_stops_kind_check;
ALTER TABLE itinerary_stops
    ADD CONSTRAINT itinerary_stops_kind_check
    CHECK (kind IN ('stop', 'transfer', 'meal', 'base'));

CREATE INDEX IF NOT EXISTS itinerary_stops_place_id_idx
    ON itinerary_stops (place_id)
    WHERE place_id IS NOT NULL;
