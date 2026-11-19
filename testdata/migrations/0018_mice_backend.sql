-- MICE backend slice (ticket #361, migration range 0017-0029).
-- Owner: stream:mice.
--
-- Additive only: everything here is a new column, table or index. The legacy
-- `events.type` / `events.venue` / `events.organizer` columns stay readable
-- until the MICE cutover is complete; new writes keep them in sync so older
-- clients keep working.

-- ---------------------------------------------------------------------------
-- Event authoring fields the frozen contract needs but 0017 did not add.
-- ---------------------------------------------------------------------------

-- EventWrite.type is an editorial theme, not the MICE class. Legacy rows keep
-- the theme in `type`; anything outside the legacy check is stored here.
ALTER TABLE events ADD COLUMN IF NOT EXISTS editorial_theme text;

-- EventDetail.highlights
ALTER TABLE events ADD COLUMN IF NOT EXISTS highlights jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Discovery filters need the county GID on the event, not only on the venue.
ALTER TABLE events ADD COLUMN IF NOT EXISTS county_gid integer
    REFERENCES kenya_counties(gid) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS events_county_gid_idx ON events (county_gid, event_date);

-- `events.created_by` is UUID and predates text user ids. Author attribution
-- for reviewer != uploader lives here.
ALTER TABLE events ADD COLUMN IF NOT EXISTS author_ref text REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS events_author_idx ON events (author_ref) WHERE author_ref IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Saved events (shortlist). The legacy `event_saves.user_id` is UUID while
-- users.id is text, so the MICE cutover needs a text-keyed table.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS event_saved_events (
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, event_id)
);
CREATE INDEX IF NOT EXISTS event_saved_events_user_idx
    ON event_saved_events (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Waitlist promotion bookkeeping. `confirm_by` is the 24h window; this marks
-- the notification as sent so a lazy expiry pass never double-notifies.
-- ---------------------------------------------------------------------------

ALTER TABLE event_registrations
    ADD COLUMN IF NOT EXISTS promotion_notified_at timestamptz;

CREATE INDEX IF NOT EXISTS event_registrations_confirm_by_idx
    ON event_registrations (event_id, confirm_by)
    WHERE status = 'confirmed' AND confirm_by IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Staff scanner lookup: one active assignment per scanner per organizer.
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS organizer_staff_user_active_idx
    ON organizer_staff (user_id, organizer_id)
    WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Verification expiry/appeal support (ADR 0012 §7). Verification is annual.
-- ---------------------------------------------------------------------------

ALTER TABLE organizer_profiles
    ADD COLUMN IF NOT EXISTS expiry_reminder_sent_at timestamptz;
ALTER TABLE organizer_profiles
    ADD COLUMN IF NOT EXISTS appeal_deadline timestamptz;

CREATE INDEX IF NOT EXISTS organizer_profiles_expiry_idx
    ON organizer_profiles (verification_expires_at)
    WHERE verification_status = 'verified';

-- ---------------------------------------------------------------------------
-- Event reports: dedupe identical open reports from the same reporter so a
-- reader cannot flood the queue with the same reason.
-- ---------------------------------------------------------------------------

CREATE UNIQUE INDEX IF NOT EXISTS event_reports_open_uniq
    ON event_reports (
        (COALESCE(event_id, '00000000-0000-0000-0000-000000000000'::uuid)),
        (COALESCE(organizer_id, '00000000-0000-0000-0000-000000000000'::uuid)),
        reporter_id,
        reason
    )
    WHERE status = 'open';
