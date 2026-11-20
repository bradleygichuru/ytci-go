-- Foreground presence for a shared trip. One row per (trip, user); the roster is
-- authoritative from Postgres so it survives instance changes and deploys.

CREATE TABLE itinerary_presence (
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'plan',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (itinerary_id, user_id)
);

CREATE INDEX itinerary_presence_last_seen_idx ON itinerary_presence (last_seen_at);
