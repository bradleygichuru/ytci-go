-- Recognition contract gate (ticket #359, migration range 0030-0042).
-- Owner: stream:recognition (backend #365, admin #367, mobile #366).
-- ADR 0010/0015. Append-only ledger plus projections in a dedicated schema.
-- There is no broker: the outbox is claimed with FOR UPDATE SKIP LOCKED.

CREATE SCHEMA IF NOT EXISTS recognition;

-- ---------------------------------------------------------------------------
-- CloudEvents envelope and event-type registry
-- ---------------------------------------------------------------------------

-- Immutable emitted facts. `id` is a UUIDv7 supplied by the emitter.
CREATE TABLE IF NOT EXISTS recognition.events (
    id                   uuid PRIMARY KEY,
    specversion          text NOT NULL DEFAULT '1.0',
    source               text NOT NULL,
    type                 text NOT NULL,
    subject              text NOT NULL,
    time                 timestamptz NOT NULL,
    received_at          timestamptz NOT NULL DEFAULT now(),
    data                 jsonb NOT NULL,
    idempotency_key      text NOT NULL,
    occurrence_key       text NOT NULL,
    sensitivity          text NOT NULL DEFAULT 'standard'
                           CHECK (sensitivity IN ('standard','sensitive','restricted')),
    privacy_class        text NOT NULL DEFAULT 'private'
                           CHECK (privacy_class IN ('private','public','sensitive')),
    age_band             text CHECK (age_band IN ('under_18','18_plus','unknown')),
    program_scope        text NOT NULL DEFAULT 'ytci',
    consent_version      text,
    verification_method  text,
    verification_result  text,
    schema_version       integer NOT NULL DEFAULT 1,
    UNIQUE (source, id),
    CONSTRAINT recognition_events_time_check CHECK (time <= received_at + interval '24 hours')
);
CREATE INDEX IF NOT EXISTS recognition_events_subject_idx
    ON recognition.events (subject, time DESC);
CREATE INDEX IF NOT EXISTS recognition_events_type_idx
    ON recognition.events (type, time DESC);
CREATE INDEX IF NOT EXISTS recognition_events_occurrence_idx
    ON recognition.events (subject, occurrence_key);

CREATE OR REPLACE FUNCTION recognition.events_immutable()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'recognition.events is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS recognition_events_immutable_trg ON recognition.events;
CREATE TRIGGER recognition_events_immutable_trg
    BEFORE UPDATE OR DELETE ON recognition.events
    FOR EACH ROW EXECUTE FUNCTION recognition.events_immutable();

-- Machine-readable registry mirroring api/contracts/cloudevents.v1.yaml.
CREATE TABLE IF NOT EXISTS recognition.event_types (
    type            text PRIMARY KEY,
    category        text NOT NULL,
    subject_type    text NOT NULL,
    occurrence_key  text NOT NULL,
    description     text NOT NULL,
    status          text NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','deprecated','retired')),
    schema_version  integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now()
);

INSERT INTO recognition.event_types (type, category, subject_type, occurrence_key, description)
VALUES
    ('ke.heritage.visit.verified.v1', 'heritage', 'site',
     'heritage.site:{site_id}', 'A verified visit to a heritage site'),
    ('ke.heritage.trail.completed.v1', 'heritage', 'trail',
     'heritage.trail:{trail_id}', 'A verified heritage trail completion'),
    ('ke.heritage.county.collected.v1', 'heritage', 'county',
     'heritage.county:{county_gid}', 'First verified site in a county'),
    ('ke.conservation.participation.verified.v1', 'conservation', 'activity',
     'conservation.activity:{activity_id}', 'Verified conservation participation'),
    ('ke.challenge.completed.v1', 'challenge', 'challenge',
     'challenge:{challenge_id}', 'A moderated challenge completion'),
    ('ke.course.completed.v1', 'course', 'course',
     'course:{course_id}:{revision}', 'A course completed with a passed quiz'),
    ('ke.event.attended.v1', 'events', 'event',
     'event.attendance:{event_id}', 'A staff or QR verified event check-in'),
    ('ke.audio.story.completed.v1', 'audio', 'story',
     'audio.story:{story_id}', 'A server-recorded Audio Story completion'),
    ('ke.community.story.approved.v1', 'community', 'story',
     'community.story:{story_id}', 'A moderated community story approval'),
    ('ke.explore.destination.progressed.v1', 'explore', 'destination',
     'explore.destination:{destination_slug}', 'Private destination progress'),
    ('ke.season.completed.v1', 'season', 'season',
     'season:{season_id}', 'A completed recognition season')
ON CONFLICT (type) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Rules, badges, awards, XP
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS recognition.badge_definitions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    badge_key        text NOT NULL UNIQUE,
    name_en          text NOT NULL,
    name_sw          text,
    family           text NOT NULL,
    criteria         jsonb NOT NULL DEFAULT '{}'::jsonb,
    icon_key         text,
    colour_token     text,
    privacy_class    text NOT NULL DEFAULT 'private'
                       CHECK (privacy_class IN ('private','public','sensitive')),
    hidden           boolean NOT NULL DEFAULT false,
    public_eligible  boolean NOT NULL DEFAULT false,
    status           text NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft','published','retired')),
    version          integer NOT NULL DEFAULT 1,
    created_by       text REFERENCES users(id) ON DELETE SET NULL,
    published_by     text REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recognition_badge_translation_check
        CHECK (status <> 'published' OR (name_en <> '' AND name_sw IS NOT NULL AND name_sw <> ''))
);

CREATE TABLE IF NOT EXISTS recognition.rules (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_key         text NOT NULL,
    version          integer NOT NULL DEFAULT 1,
    source_type      text NOT NULL,
    status           text NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft','published','retired')),
    effective_from   timestamptz NOT NULL DEFAULT now(),
    effective_to     timestamptz,
    criteria         jsonb NOT NULL DEFAULT '{}'::jsonb,
    xp_amount        integer NOT NULL DEFAULT 0 CHECK (xp_amount >= 0),
    badge_key        text REFERENCES recognition.badge_definitions(badge_key)
                       ON DELETE SET NULL,
    created_by       text REFERENCES users(id) ON DELETE SET NULL,
    published_by     text REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (rule_key, version),
    CONSTRAINT recognition_rules_window_check
        CHECK (effective_to IS NULL OR effective_to > effective_from)
);

CREATE TABLE IF NOT EXISTS recognition.badge_awards (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id       text NOT NULL,
    badge_key        text NOT NULL REFERENCES recognition.badge_definitions(badge_key),
    rule_key         text NOT NULL,
    rule_version     integer NOT NULL,
    source_type      text NOT NULL,
    source_id        text NOT NULL,
    event_id         uuid REFERENCES recognition.events(id) ON DELETE SET NULL,
    privacy_class    text NOT NULL DEFAULT 'private'
                       CHECK (privacy_class IN ('private','public','sensitive')),
    legacy           boolean NOT NULL DEFAULT false,
    hidden           boolean NOT NULL DEFAULT false,
    awarded_at       timestamptz NOT NULL DEFAULT now(),
    revoked_at       timestamptz,
    revoke_reason    text
);
CREATE UNIQUE INDEX IF NOT EXISTS recognition_badge_awards_source_key
    ON recognition.badge_awards (subject_id, badge_key, source_type, source_id);
CREATE INDEX IF NOT EXISTS recognition_badge_awards_subject_idx
    ON recognition.badge_awards (subject_id, awarded_at DESC);

CREATE TABLE IF NOT EXISTS recognition.xp_entries (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id        text NOT NULL,
    amount            integer NOT NULL,
    reason            text NOT NULL CHECK (reason IN ('award','adjustment','reversal')),
    rule_key          text NOT NULL,
    rule_version      integer NOT NULL,
    source_type       text NOT NULL,
    source_id         text NOT NULL,
    event_id          uuid REFERENCES recognition.events(id) ON DELETE SET NULL,
    compensating_for  uuid REFERENCES recognition.xp_entries(id) ON DELETE SET NULL,
    approved_by       text REFERENCES users(id) ON DELETE SET NULL,
    note              text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recognition_xp_adjustment_check
        CHECK (reason = 'award' OR approved_by IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS recognition_xp_entries_award_key
    ON recognition.xp_entries (subject_id, rule_key, source_type, source_id)
    WHERE reason = 'award';
CREATE INDEX IF NOT EXISTS recognition_xp_entries_subject_idx
    ON recognition.xp_entries (subject_id, created_at DESC);

-- Only subject anonymisation is allowed; amounts and keys never change.
CREATE OR REPLACE FUNCTION recognition.xp_entries_guard()
RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'recognition.xp_entries is append-only';
    END IF;
    IF (to_jsonb(OLD) - 'subject_id') <> (to_jsonb(NEW) - 'subject_id') THEN
        RAISE EXCEPTION 'recognition.xp_entries is append-only except subject anonymisation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS recognition_xp_entries_guard_trg ON recognition.xp_entries;
CREATE TRIGGER recognition_xp_entries_guard_trg
    BEFORE UPDATE OR DELETE ON recognition.xp_entries
    FOR EACH ROW EXECUTE FUNCTION recognition.xp_entries_guard();

CREATE TABLE IF NOT EXISTS recognition.xp_totals (
    subject_id      text PRIMARY KEY,
    total_xp        bigint NOT NULL DEFAULT 0,
    level           integer NOT NULL DEFAULT 0,
    highest_level   integer NOT NULL DEFAULT 0,
    reconciled_at   timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recognition.levels (
    level       integer PRIMARY KEY,
    name_en     text NOT NULL,
    name_sw     text NOT NULL,
    threshold   integer NOT NULL UNIQUE CHECK (threshold >= 0)
);

INSERT INTO recognition.levels (level, name_en, name_sw, threshold)
VALUES
    (0, 'Newcomer',  'Mwanzo',      0),
    (1, 'Traveler',  'Msafiri',     250),
    (2, 'Pathfinder','Mgunduzi',    750),
    (3, 'Guardian',  'Mlinzi',      1500),
    (4, 'Keeper',    'Mtunza',      3000)
ON CONFLICT (level) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Progress, missions, seasons, leaderboard
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS recognition.progress_tracks (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    track_key      text NOT NULL UNIQUE,
    family         text NOT NULL
                     CHECK (family IN ('trail','county_collection','course_path','mission')),
    name_en        text NOT NULL,
    name_sw        text,
    criteria       jsonb NOT NULL DEFAULT '{}'::jsonb,
    xp_reward      integer NOT NULL DEFAULT 0 CHECK (xp_reward >= 0),
    badge_key      text REFERENCES recognition.badge_definitions(badge_key)
                     ON DELETE SET NULL,
    status         text NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft','published','retired')),
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recognition.progress_snapshots (
    subject_id     text NOT NULL,
    track_key      text NOT NULL REFERENCES recognition.progress_tracks(track_key),
    current_value  integer NOT NULL DEFAULT 0,
    target_value   integer NOT NULL CHECK (target_value > 0),
    completed_at   timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_id, track_key)
);

CREATE TABLE IF NOT EXISTS recognition.seasons (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_key     text NOT NULL UNIQUE,
    name_en        text NOT NULL,
    name_sw        text,
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    status         text NOT NULL DEFAULT 'upcoming'
                     CHECK (status IN ('upcoming','active','frozen','closed')),
    badge_key      text REFERENCES recognition.badge_definitions(badge_key)
                     ON DELETE SET NULL,
    xp_award       integer NOT NULL DEFAULT 500 CHECK (xp_award >= 0),
    created_by     text REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recognition_seasons_window_check CHECK (ends_at > starts_at)
);

-- Public payload: display name, county, level, public badge count, verified
-- sites, trail completions. Never XP total or user_id.
CREATE TABLE IF NOT EXISTS recognition.leaderboard_entries (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id          uuid NOT NULL REFERENCES recognition.seasons(id) ON DELETE CASCADE,
    subject_id         text NOT NULL,
    county_gid         integer REFERENCES kenya_counties(gid) ON DELETE SET NULL,
    display_name       text NOT NULL,
    level              integer NOT NULL DEFAULT 0,
    public_badge_count integer NOT NULL DEFAULT 0,
    verified_sites     integer NOT NULL DEFAULT 0,
    trail_completions  integer NOT NULL DEFAULT 0,
    rank_position      integer,
    opted_in           boolean NOT NULL DEFAULT false,
    suppressed         boolean NOT NULL DEFAULT false,
    disqualified       boolean NOT NULL DEFAULT false,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (season_id, subject_id)
);
CREATE INDEX IF NOT EXISTS recognition_leaderboard_board_idx
    ON recognition.leaderboard_entries (season_id, county_gid, rank_position)
    WHERE opted_in AND NOT suppressed AND NOT disqualified;

CREATE TABLE IF NOT EXISTS recognition.leaderboard_snapshots (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id      uuid NOT NULL REFERENCES recognition.seasons(id) ON DELETE CASCADE,
    county_gid     integer,
    taken_at       timestamptz NOT NULL DEFAULT now(),
    entry_count    integer NOT NULL,
    payload        jsonb NOT NULL,
    retain_until   timestamptz NOT NULL DEFAULT now() + interval '12 months'
);

CREATE TABLE IF NOT EXISTS recognition.display_names (
    subject_id          text PRIMARY KEY,
    display_name        text NOT NULL,
    status              text NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending','approved','rejected')),
    previous_approved   text,
    moderated_by        text REFERENCES users(id) ON DELETE SET NULL,
    moderation_reason   text,
    changed_at          timestamptz NOT NULL DEFAULT now(),
    next_change_allowed timestamptz,
    CONSTRAINT recognition_display_name_length_check
        CHECK (char_length(display_name) BETWEEN 2 AND 24)
);

CREATE TABLE IF NOT EXISTS recognition.moderation_cases (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             text NOT NULL CHECK (kind IN
                       ('display_name','leaderboard_cheat','leaderboard_report','event_report')),
    subject_id       text,
    reported_by      text REFERENCES users(id) ON DELETE SET NULL,
    reason           text NOT NULL,
    evidence         jsonb NOT NULL DEFAULT '{}'::jsonb,
    status           text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open','triaged','decided','appealed','closed')),
    decision         text CHECK (decision IN ('none','warn','disqualify','rename','dismiss','revoke')),
    decided_by       text REFERENCES users(id) ON DELETE SET NULL,
    decided_at       timestamptz,
    appeal_deadline  timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS recognition_moderation_cases_queue_idx
    ON recognition.moderation_cases (status, created_at) WHERE status IN ('open','triaged','appealed');

CREATE TABLE IF NOT EXISTS recognition.leaderboard_reports (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id      uuid NOT NULL REFERENCES recognition.seasons(id) ON DELETE CASCADE,
    subject_id     text NOT NULL,
    reporter_id    text REFERENCES users(id) ON DELETE SET NULL,
    reason         text NOT NULL CHECK (reason IN
                     ('fraud','abuse','impersonation','harassment','display_name')),
    details        text,
    case_id        uuid REFERENCES recognition.moderation_cases(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS recognition_leaderboard_reports_queue_idx
    ON recognition.leaderboard_reports (created_at);

CREATE TABLE IF NOT EXISTS recognition.disqualifications (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id      uuid REFERENCES recognition.seasons(id) ON DELETE CASCADE,
    subject_id     text NOT NULL,
    reason         text NOT NULL CHECK (reason IN ('fraud','abuse','impersonation','harassment')),
    evidence       jsonb NOT NULL DEFAULT '{}'::jsonb,
    case_id        uuid REFERENCES recognition.moderation_cases(id) ON DELETE SET NULL,
    decided_by     text REFERENCES users(id) ON DELETE SET NULL,
    decided_at     timestamptz NOT NULL DEFAULT now(),
    appeal_deadline timestamptz NOT NULL DEFAULT now() + interval '14 days',
    appeal_outcome text,
    revoked_at     timestamptz
);

CREATE TABLE IF NOT EXISTS recognition.retractions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id     text NOT NULL,
    entity_type    text NOT NULL CHECK (entity_type IN ('badge_award','leaderboard_entry','site')),
    entity_id      text NOT NULL,
    reason         text NOT NULL,
    decided_by     text REFERENCES users(id) ON DELETE SET NULL,
    decided_at     timestamptz NOT NULL DEFAULT now(),
    notified_at    timestamptz
);

-- Scoped recognition consent read model. `consent_records` remains the
-- account-level audit; the engine reads the latest granted row per scope.
CREATE TABLE IF NOT EXISTS recognition.consents (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id     text NOT NULL,
    scope          text NOT NULL CHECK (scope IN
                     ('recognition','location_verification','public_passport',
                      'leaderboard','analytics','notifications')),
    version        text NOT NULL,
    state          text NOT NULL CHECK (state IN ('unknown','granted','denied')),
    source         text NOT NULL DEFAULT 'app',
    recorded_at    timestamptz NOT NULL DEFAULT now(),
    withdrawn_at   timestamptz
);
CREATE INDEX IF NOT EXISTS recognition_consents_subject_idx
    ON recognition.consents (subject_id, scope, recorded_at DESC);

CREATE TABLE IF NOT EXISTS recognition.kill_switches (
    key            text PRIMARY KEY,
    enabled        boolean NOT NULL DEFAULT false,
    reason         text,
    updated_by     text REFERENCES users(id) ON DELETE SET NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

INSERT INTO recognition.kill_switches (key, enabled, reason)
VALUES
    ('global', false, 'recognition dark launch'),
    ('public_leaderboard', false, 'DPIA and 18+ pilot gate'),
    ('source:event', false, 'waiting for event check-in source'),
    ('source:audio', false, 'waiting for playback telemetry')
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Delivery, backfill, audit
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS recognition.outbox (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid NOT NULL REFERENCES recognition.events(id) ON DELETE CASCADE,
    topic          text NOT NULL DEFAULT 'rules.evaluate',
    status         text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','processing','done','dead')),
    attempts       integer NOT NULL DEFAULT 0,
    available_at   timestamptz NOT NULL DEFAULT now(),
    locked_at      timestamptz,
    worker_id      text,
    last_error     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    processed_at   timestamptz,
    UNIQUE (event_id, topic)
);
CREATE INDEX IF NOT EXISTS recognition_outbox_due_idx
    ON recognition.outbox (available_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS recognition.backfill_batches (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_key       text NOT NULL,
    rule_version   integer NOT NULL,
    filter         jsonb NOT NULL DEFAULT '{}'::jsonb,
    status         text NOT NULL DEFAULT 'dry_run'
                     CHECK (status IN ('dry_run','approved','running','paused','completed','failed')),
    dry_run_diff   jsonb,
    processed      integer NOT NULL DEFAULT 0,
    awarded        integer NOT NULL DEFAULT 0,
    created_by     text NOT NULL REFERENCES users(id),
    approved_by    text REFERENCES users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    finished_at    timestamptz,
    CONSTRAINT recognition_backfill_second_approver_check
        CHECK (status IN ('dry_run','approved','running','paused','completed','failed')
               AND (approved_by IS NULL OR approved_by <> created_by))
);

CREATE TABLE IF NOT EXISTS recognition.audit (
    id             bigserial PRIMARY KEY,
    actor_id       text REFERENCES users(id) ON DELETE SET NULL,
    action         text NOT NULL,
    entity_type    text NOT NULL,
    entity_id      text,
    before         jsonb,
    after          jsonb,
    reason         text,
    request_id     text,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS recognition_audit_entity_idx
    ON recognition.audit (entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS recognition_audit_actor_idx
    ON recognition.audit (actor_id, created_at DESC);
