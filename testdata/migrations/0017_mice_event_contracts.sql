-- MICE / events contract gate (ticket #359, migration range 0017-0029).
-- Owner: stream:mice (backend #361, admin #363, mobile #362).
-- Additive only. Legacy `events.venue`, `events.organizer` and `event_attendees`
-- remain readable until the MICE backend slice completes the cutover.
--
-- Canonical vocabulary lives in docs/execution/contracts.md and
-- api/contracts/mice.v1.yaml. `events.status = 'scheduled'` is a deprecated
-- legacy alias of `published`; new writes must use the canonical values.

-- ---------------------------------------------------------------------------
-- Organizer profiles and trust (ADR 0012)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS organizer_profiles (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                     text NOT NULL UNIQUE,
    display_name             text NOT NULL,
    trust_tier               text CHECK (trust_tier IN
                               ('programme_partner','verified_organizer','community_organizer')),
    verification_status      text NOT NULL DEFAULT 'unverified'
                               CHECK (verification_status IN
                               ('unverified','pending','verified','expired','revoked')),
    about                    text,
    logo_url                 text,
    contact_email            text,
    contact_phone            text,
    website_url              text,
    partner_business_id      uuid REFERENCES partner_businesses(id) ON DELETE SET NULL,
    legal_name               text,
    registration_number      text,
    verified_at              timestamptz,
    verification_expires_at  timestamptz,
    revoked_at               timestamptz,
    revoked_reason           text,
    created_by               text REFERENCES users(id) ON DELETE SET NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizer_profiles_verified_tier_check CHECK (
        verification_status <> 'verified' OR trust_tier IS NOT NULL
    ),
    CONSTRAINT organizer_profiles_verified_date_check CHECK (
        verification_status <> 'verified' OR verified_at IS NOT NULL
    )
);
CREATE INDEX IF NOT EXISTS organizer_profiles_status_idx
    ON organizer_profiles (verification_status, verification_expires_at);
CREATE INDEX IF NOT EXISTS organizer_profiles_tier_idx
    ON organizer_profiles (trust_tier) WHERE verification_status = 'verified';

-- Append-only trust history. Reviewer != requester is enforced by the
-- application and audited here.
CREATE TABLE IF NOT EXISTS organizer_verification_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id   uuid NOT NULL REFERENCES organizer_profiles(id) ON DELETE CASCADE,
    action         text NOT NULL CHECK (action IN
                     ('submitted','verified','rejected','expired','revoked',
                      'restored','appeal_filed','appeal_decided')),
    from_status    text,
    to_status      text,
    from_tier      text,
    to_tier        text,
    actor_id       text REFERENCES users(id) ON DELETE SET NULL,
    reason         text,
    evidence_ref   text,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS organizer_verification_events_org_idx
    ON organizer_verification_events (organizer_id, created_at DESC);

-- Restricted evidence metadata. The bytes live in restricted, access-logged
-- storage; this table never points at the public media bucket.
CREATE TABLE IF NOT EXISTS organizer_verification_evidence (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id   uuid NOT NULL REFERENCES organizer_profiles(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN
                     ('legal_registration','government_id','partner_agreement',
                      'venue_relationship','verified_contact')),
    storage_key    text NOT NULL,
    sha256         text NOT NULL,
    expires_at     timestamptz,
    reviewed_by    text REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS organizer_verification_evidence_org_idx
    ON organizer_verification_evidence (organizer_id, created_at DESC);

-- Staff scanner assignment lives on the organizer profile, not on a duplicate
-- user account (ADR 0011 §9).
CREATE TABLE IF NOT EXISTS organizer_staff (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id   uuid NOT NULL REFERENCES organizer_profiles(id) ON DELETE CASCADE,
    user_id        text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    staff_role     text NOT NULL CHECK (staff_role IN ('scanner','manager')),
    assigned_by    text REFERENCES users(id) ON DELETE SET NULL,
    assigned_at    timestamptz NOT NULL DEFAULT now(),
    revoked_at     timestamptz,
    revoked_by     text REFERENCES users(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS organizer_staff_active_uniq
    ON organizer_staff (organizer_id, user_id, staff_role)
    WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Venues and accessibility (ADR 0009 §2, §7)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS venues (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                   text NOT NULL UNIQUE,
    name                   text NOT NULL,
    address_line1          text,
    address_line2          text,
    county                 text,
    county_gid             integer REFERENCES kenya_counties(gid) ON DELETE SET NULL,
    latitude               numeric(10,8),
    longitude              numeric(11,8),
    capacity_in_person     integer CHECK (capacity_in_person IS NULL OR capacity_in_person >= 0),
    capacity_online        integer CHECK (capacity_online IS NULL OR capacity_online >= 0),
    rooms                  jsonb NOT NULL DEFAULT '[]'::jsonb
                             CHECK (jsonb_typeof(rooms) = 'array'),
    contact_email          text,
    contact_phone          text,
    transport_notes        text,
    dropoff_notes          text,
    parking_notes          text,
    destination_slug       text REFERENCES destinations(slug) ON DELETE SET NULL,
    verified_at            timestamptz,
    created_by             text REFERENCES users(id) ON DELETE SET NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS venues_county_idx ON venues (county_gid, name);
CREATE INDEX IF NOT EXISTS venues_verified_idx ON venues (verified_at DESC);

CREATE TABLE IF NOT EXISTS venue_accessibility (
    venue_id                 uuid PRIMARY KEY REFERENCES venues(id) ON DELETE CASCADE,
    step_free_entrance       text NOT NULL DEFAULT 'unknown'
                               CHECK (step_free_entrance IN ('yes','no','unknown')),
    step_free_route          text NOT NULL DEFAULT 'unknown'
                               CHECK (step_free_route IN ('yes','no','unknown')),
    accessible_toilet        text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_toilet IN ('yes','no','unknown')),
    accessible_parking       text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_parking IN ('yes','no','unknown')),
    accessible_seating       text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_seating IN ('yes','no','unknown')),
    captioning               text NOT NULL DEFAULT 'unknown'
                               CHECK (captioning IN ('yes','no','unknown')),
    sign_language            text NOT NULL DEFAULT 'unknown'
                               CHECK (sign_language IN ('yes','no','unknown')),
    hearing_loop             text NOT NULL DEFAULT 'unknown'
                               CHECK (hearing_loop IN ('yes','no','unknown')),
    quiet_room               text NOT NULL DEFAULT 'unknown'
                               CHECK (quiet_room IN ('yes','no','unknown')),
    assistance_dogs          text NOT NULL DEFAULT 'unknown'
                               CHECK (assistance_dogs IN ('yes','no','unknown')),
    companion_ticket_policy  text,
    content_notes            text,
    reported_by              text,
    verified_at              timestamptz,
    updated_at               timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Event columns (ADR 0009 §1, §8, §9)
-- ---------------------------------------------------------------------------

ALTER TABLE events ADD COLUMN IF NOT EXISTS event_class text NOT NULL DEFAULT 'community';
ALTER TABLE events ADD COLUMN IF NOT EXISTS format text NOT NULL DEFAULT 'in_person';
ALTER TABLE events ADD COLUMN IF NOT EXISTS access text NOT NULL DEFAULT 'public';
ALTER TABLE events ADD COLUMN IF NOT EXISTS venue_id uuid REFERENCES venues(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS organizer_profile_id uuid
    REFERENCES organizer_profiles(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS capacity integer;
ALTER TABLE events ADD COLUMN IF NOT EXISTS waitlist_capacity integer;
ALTER TABLE events ADD COLUMN IF NOT EXISTS registration_mode text NOT NULL DEFAULT 'none';
ALTER TABLE events ADD COLUMN IF NOT EXISTS price_amount numeric(10,2);
ALTER TABLE events ADD COLUMN IF NOT EXISTS price_currency char(3);
ALTER TABLE events ADD COLUMN IF NOT EXISTS sales_status text;
ALTER TABLE events ADD COLUMN IF NOT EXISTS sales_end timestamptz;
ALTER TABLE events ADD COLUMN IF NOT EXISTS ticket_url text;
ALTER TABLE events ADD COLUMN IF NOT EXISTS timezone text NOT NULL DEFAULT 'Africa/Nairobi';
ALTER TABLE events ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;
ALTER TABLE events ADD COLUMN IF NOT EXISTS published_at timestamptz;
ALTER TABLE events ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_event_class_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_event_class_check
            CHECK (event_class IN ('meeting','incentive','conference','exhibition','community'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_format_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_format_check
            CHECK (format IN ('in_person','online','hybrid'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_access_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_access_check
            CHECK (access IN ('public','registered','invite_only'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_registration_mode_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_registration_mode_check
            CHECK (registration_mode IN ('rsvp','ticket_handoff','none'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_sales_status_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_sales_status_check
            CHECK (sales_status IS NULL OR sales_status IN ('on_sale','sold_out','ended','not_on_sale'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_capacity_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_capacity_check
            CHECK (capacity IS NULL OR capacity >= 0)
            NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_price_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_price_check
            CHECK (price_amount IS NULL OR price_amount >= 0)
            NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'events_paid_handoff_check') THEN
        ALTER TABLE events ADD CONSTRAINT events_paid_handoff_check
            CHECK (price_amount IS NULL OR (price_currency IS NOT NULL AND ticket_url IS NOT NULL))
            NOT VALID;
    END IF;
END $$;

-- Canonical lifecycle vocabulary is draft | published | postponed | cancelled.
-- `scheduled` is retained as a deprecated alias for `published` so legacy
-- handlers keep working until the MICE backend lands.
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_status_check;
ALTER TABLE events ADD CONSTRAINT events_status_check
    CHECK (status IN ('scheduled','draft','published','postponed','cancelled'));

CREATE INDEX IF NOT EXISTS events_discovery_idx
    ON events (access, status, event_date)
    WHERE access <> 'invite_only';
CREATE INDEX IF NOT EXISTS events_class_idx ON events (event_class, event_date);
CREATE INDEX IF NOT EXISTS events_organizer_idx ON events (organizer_profile_id, event_date DESC);
CREATE INDEX IF NOT EXISTS events_venue_idx ON events (venue_id, event_date DESC);

CREATE TABLE IF NOT EXISTS event_accessibility (
    event_id                 uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    step_free_entrance       text NOT NULL DEFAULT 'unknown'
                               CHECK (step_free_entrance IN ('yes','no','unknown')),
    step_free_route          text NOT NULL DEFAULT 'unknown'
                               CHECK (step_free_route IN ('yes','no','unknown')),
    accessible_toilet        text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_toilet IN ('yes','no','unknown')),
    accessible_parking       text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_parking IN ('yes','no','unknown')),
    accessible_seating       text NOT NULL DEFAULT 'unknown'
                               CHECK (accessible_seating IN ('yes','no','unknown')),
    captioning               text NOT NULL DEFAULT 'unknown'
                               CHECK (captioning IN ('yes','no','unknown')),
    sign_language            text NOT NULL DEFAULT 'unknown'
                               CHECK (sign_language IN ('yes','no','unknown')),
    hearing_loop             text NOT NULL DEFAULT 'unknown'
                               CHECK (hearing_loop IN ('yes','no','unknown')),
    quiet_room               text NOT NULL DEFAULT 'unknown'
                               CHECK (quiet_room IN ('yes','no','unknown')),
    assistance_dogs          text NOT NULL DEFAULT 'unknown'
                               CHECK (assistance_dogs IN ('yes','no','unknown')),
    companion_ticket_policy  text,
    content_notes            text,
    reported_by              text,
    verified_at              timestamptz,
    updated_at               timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Agenda (ADR 0009 §3)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS event_sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    day            date NOT NULL,
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    room           text,
    track          text,
    title          text NOT NULL,
    description    text,
    capacity       integer CHECK (capacity IS NULL OR capacity >= 0),
    display_order  integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT event_sessions_time_check CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS event_sessions_event_idx
    ON event_sessions (event_id, day, starts_at);

CREATE TABLE IF NOT EXISTS event_people (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name   text NOT NULL,
    title          text,
    organization   text,
    bio            text,
    photo_url      text,
    is_public      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS session_speakers (
    session_id     uuid NOT NULL REFERENCES event_sessions(id) ON DELETE CASCADE,
    person_id      uuid NOT NULL REFERENCES event_people(id) ON DELETE CASCADE,
    speaker_role   text NOT NULL DEFAULT 'speaker'
                     CHECK (speaker_role IN ('speaker','moderator','panelist','host')),
    display_order  integer NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, person_id)
);
CREATE INDEX IF NOT EXISTS session_speakers_person_idx ON session_speakers (person_id);

-- ---------------------------------------------------------------------------
-- Registration, ticket, check-in (ADR 0009 §4-§5, ADR 0011 §1-§5)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS event_registration_questions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    prompt_en      text NOT NULL,
    prompt_sw      text,
    answer_kind    text NOT NULL DEFAULT 'short_text'
                     CHECK (answer_kind IN ('short_text','single_choice','boolean')),
    choices        text[] NOT NULL DEFAULT '{}',
    required       boolean NOT NULL DEFAULT false,
    display_order  integer NOT NULL DEFAULT 0,
    UNIQUE (event_id, display_order)
);

CREATE OR REPLACE FUNCTION event_registration_questions_limit()
RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM event_registration_questions WHERE event_id = NEW.event_id) >= 2 THEN
        RAISE EXCEPTION 'at most two registration questions per event';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS event_registration_questions_limit_trg ON event_registration_questions;
CREATE TRIGGER event_registration_questions_limit_trg
    BEFORE INSERT ON event_registration_questions
    FOR EACH ROW EXECUTE FUNCTION event_registration_questions_limit();

CREATE TABLE IF NOT EXISTS event_registrations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id              uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id               text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status                text NOT NULL DEFAULT 'confirmed'
                            CHECK (status IN ('confirmed','waitlisted','cancelled','checked_in')),
    answers               jsonb NOT NULL DEFAULT '{}'::jsonb
                            CHECK (jsonb_typeof(answers) = 'object'),
    reminder_offset_minutes integer CHECK (reminder_offset_minutes IN (60, 1440, 10080)),
    waitlist_position     integer,
    confirm_by            timestamptz,
    cancel_reason         text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    cancelled_at          timestamptz,
    checked_in_at         timestamptz,
    UNIQUE (event_id, user_id)
);
CREATE INDEX IF NOT EXISTS event_registrations_event_status_idx
    ON event_registrations (event_id, status, created_at);
CREATE INDEX IF NOT EXISTS event_registrations_user_idx
    ON event_registrations (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS event_registrations_waitlist_idx
    ON event_registrations (event_id, waitlist_position)
    WHERE status = 'waitlisted';

-- Legacy backfill: one registration per legacy attendee, resolving the
-- multi-status defect by choosing the strongest state.
INSERT INTO event_registrations (event_id, user_id, status, created_at)
SELECT event_id, user_id,
       CASE
           WHEN bool_or(status = 'joined') THEN 'confirmed'
           WHEN bool_or(status = 'waitlist') THEN 'waitlisted'
           ELSE 'cancelled'
       END,
       min(created_at)
FROM event_attendees
GROUP BY event_id, user_id
ON CONFLICT (event_id, user_id) DO NOTHING;

-- A paid event records handoff interest only. It never creates a confirmed
-- registration, a ticket, or a capacity claim (ADR 0011 §2).
CREATE TABLE IF NOT EXISTS event_ticket_handoffs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id    uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source      text NOT NULL DEFAULT 'mobile' CHECK (source IN ('mobile','web','admin')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, user_id)
);
CREATE INDEX IF NOT EXISTS event_ticket_handoffs_event_idx
    ON event_ticket_handoffs (event_id, created_at DESC);

CREATE TABLE IF NOT EXISTS event_tickets (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    registration_id  uuid NOT NULL UNIQUE REFERENCES event_registrations(id) ON DELETE CASCADE,
    token_hash       text NOT NULL UNIQUE,
    issued_at        timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    revoked_at       timestamptz,
    revoked_reason   text,
    CONSTRAINT event_tickets_expiry_check CHECK (expires_at > issued_at)
);

CREATE TABLE IF NOT EXISTS event_checkins (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    registration_id  uuid NOT NULL UNIQUE REFERENCES event_registrations(id) ON DELETE CASCADE,
    event_id         uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checked_in_at    timestamptz NOT NULL DEFAULT now(),
    method           text NOT NULL
                       CHECK (method IN ('qr','manual','offline_queue','staff_override')),
    staff_user_id    text REFERENCES users(id) ON DELETE SET NULL,
    device_id        text,
    client_ts        timestamptz,
    override_reason  text,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_checkins_event_idx
    ON event_checkins (event_id, checked_in_at DESC);
CREATE INDEX IF NOT EXISTS event_checkins_user_idx
    ON event_checkins (user_id, checked_in_at DESC);

-- Offline scanner queue. The device posts scans; the server resolves
-- first-write-wins and flags duplicates for review.
CREATE TABLE IF NOT EXISTS event_checkin_scans (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id          uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    device_id         text NOT NULL,
    client_scan_id    text NOT NULL,
    token_hash        text,
    registration_id   uuid REFERENCES event_registrations(id) ON DELETE SET NULL,
    staff_user_id     text REFERENCES users(id) ON DELETE SET NULL,
    client_ts         timestamptz NOT NULL,
    received_at       timestamptz NOT NULL DEFAULT now(),
    status            text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','applied','duplicate','rejected')),
    reason            text,
    resolved_at       timestamptz,
    UNIQUE (device_id, client_scan_id)
);
CREATE INDEX IF NOT EXISTS event_checkin_scans_event_idx
    ON event_checkin_scans (event_id, received_at DESC);
CREATE INDEX IF NOT EXISTS event_checkin_scans_pending_idx
    ON event_checkin_scans (event_id) WHERE status IN ('pending','duplicate');

-- ---------------------------------------------------------------------------
-- Reporting (ADR 0012 §6)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS event_reports (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        uuid REFERENCES events(id) ON DELETE CASCADE,
    organizer_id    uuid REFERENCES organizer_profiles(id) ON DELETE CASCADE,
    reporter_id     text REFERENCES users(id) ON DELETE SET NULL,
    reason          text NOT NULL CHECK (reason IN
                      ('impersonation','safety_concern','inaccurate_information',
                       'spam','payment_issue','accessibility_misrepresentation')),
    details         text,
    status          text NOT NULL DEFAULT 'open'
                      CHECK (status IN ('open','triaged','resolved','dismissed')),
    assigned_to     text REFERENCES users(id) ON DELETE SET NULL,
    decision_note   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    CONSTRAINT event_reports_target_check CHECK (event_id IS NOT NULL OR organizer_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS event_reports_queue_idx
    ON event_reports (status, created_at) WHERE status IN ('open','triaged');

-- ---------------------------------------------------------------------------
-- Event notifications and change notices (ADR 0011 §6-§8)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS event_notifications (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id         text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            text NOT NULL
                      CHECK (kind IN ('reminder','change_notice','waitlist_promotion')),
    event_version   integer NOT NULL,
    deliver_after   timestamptz NOT NULL DEFAULT now(),
    status          text NOT NULL DEFAULT 'queued'
                      CHECK (status IN ('queued','sent','skipped','failed','read')),
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    push_ticket_id  text,
    sent_at         timestamptz,
    read_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, user_id, kind, event_version)
);
CREATE INDEX IF NOT EXISTS event_notifications_due_idx
    ON event_notifications (status, deliver_after);
CREATE INDEX IF NOT EXISTS event_notifications_user_idx
    ON event_notifications (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS event_change_notices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    from_version    integer NOT NULL,
    to_version      integer NOT NULL,
    material_fields text[] NOT NULL,
    copy_en         text NOT NULL,
    copy_sw         text,
    status          text NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft','approved','sent','cancelled')),
    created_by      text REFERENCES users(id) ON DELETE SET NULL,
    approved_by     text REFERENCES users(id) ON DELETE SET NULL,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT event_change_notices_material_check CHECK (cardinality(material_fields) > 0)
);
CREATE INDEX IF NOT EXISTS event_change_notices_event_idx
    ON event_change_notices (event_id, created_at DESC);
