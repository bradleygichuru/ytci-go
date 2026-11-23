-- MICE admin operations closure (additive).
--
-- Adds the archive lifecycle state, the speaker consent record and the
-- append-only MICE admin audit table. Existing rows keep their behaviour:
-- event_people.consent_status defaults to 'granted' so speakers that were
-- already public stay public; the server writes 'declined' on withdrawal.

-- ---------------------------------------------------------------------------
-- Archive lifecycle. Before this migration an archived event had to reuse
-- 'draft', which erased the difference between "never published" and "taken
-- down". Archived events are excluded from public discovery.
-- ---------------------------------------------------------------------------

ALTER TABLE events DROP CONSTRAINT IF EXISTS events_status_check;
ALTER TABLE events ADD CONSTRAINT events_status_check
    CHECK (status IN ('scheduled','draft','published','postponed','cancelled','archived'));

CREATE INDEX IF NOT EXISTS events_archived_idx
    ON events (event_date DESC) WHERE status = 'archived';

-- ---------------------------------------------------------------------------
-- Speaker consent. `is_public` remains the projection the public agenda reads;
-- consent_status records what the speaker actually decided so an admin can see
-- "pending" without inventing a local overlay.
-- ---------------------------------------------------------------------------

ALTER TABLE event_people
    ADD COLUMN IF NOT EXISTS consent_status text NOT NULL DEFAULT 'granted'
        CHECK (consent_status IN ('pending','granted','declined'));
ALTER TABLE event_people
    ADD COLUMN IF NOT EXISTS consent_updated_at timestamptz;
ALTER TABLE event_people
    ADD COLUMN IF NOT EXISTS consent_updated_by text REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS event_people_consent_idx
    ON event_people (consent_status);

-- ---------------------------------------------------------------------------
-- Append-only MICE admin audit. One row per admin write, readable through
-- GET /v1/admin/mice/audit. Details are free-form JSON so a row can carry the
-- before/after values without a schema change per action.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS mice_audit_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type text NOT NULL CHECK (entity_type IN
                    ('event','registration','checkin','speaker','venue','organizer','report')),
    entity_id   text NOT NULL,
    event_id    uuid REFERENCES events(id) ON DELETE SET NULL,
    action      text NOT NULL,
    actor_id    text REFERENCES users(id) ON DELETE SET NULL,
    summary     text NOT NULL,
    details     jsonb NOT NULL DEFAULT '{}'::jsonb
                    CHECK (jsonb_typeof(details) = 'object'),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS mice_audit_events_created_idx
    ON mice_audit_events (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS mice_audit_events_entity_idx
    ON mice_audit_events (entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS mice_audit_events_event_idx
    ON mice_audit_events (event_id, created_at DESC)
    WHERE event_id IS NOT NULL;
