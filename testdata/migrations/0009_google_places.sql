CREATE TABLE google_places_cache (
    place_id TEXT PRIMARY KEY NOT NULL,
    name TEXT,
    formatted_address TEXT,
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION,
    types TEXT[],
    data JSONB,
    cached_at TIMESTAMP WITH TIME ZONE DEFAULT now()
);

CREATE TABLE google_places_search_cache (
    query_hash TEXT PRIMARY KEY NOT NULL,
    response JSONB,
    cached_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    expires_at TIMESTAMP WITH TIME ZONE
);

ALTER TABLE destinations ADD COLUMN google_place_id TEXT;

CREATE UNIQUE INDEX destinations_google_place_id_unique ON destinations (google_place_id) WHERE google_place_id IS NOT NULL;
