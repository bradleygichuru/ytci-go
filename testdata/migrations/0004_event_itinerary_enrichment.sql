CREATE TABLE event_attendees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('joined', 'interested', 'waitlist', 'cancelled')),
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE event_highlights (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    icon TEXT,
    display_order INTEGER DEFAULT 0
);

ALTER TABLE events ADD COLUMN start_time TEXT;
ALTER TABLE events ADD COLUMN end_time TEXT;
ALTER TABLE events ADD COLUMN entry_fee TEXT;
ALTER TABLE events ADD COLUMN location_lat DECIMAL(10, 8);
ALTER TABLE events ADD COLUMN location_lng DECIMAL(11, 8);
ALTER TABLE events ADD COLUMN organizer_avatar_url TEXT;

ALTER TABLE itinerary_stops ADD COLUMN start_time TEXT;
ALTER TABLE itinerary_stops ADD COLUMN category TEXT;
ALTER TABLE itinerary_stops ADD COLUMN image_url TEXT;

CREATE UNIQUE INDEX event_attendees_user_event_status_idx ON event_attendees (user_id, event_id, status);
CREATE INDEX event_attendees_event_idx ON event_attendees (event_id);
