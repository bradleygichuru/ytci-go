-- Product analytics contract gate (ticket #359, migration range 0056-0068).
-- Owner: stream:analytics (backend #373, admin #374).
-- ADR 0016. Pseudonymous only: user_id never lands on a new event row.
-- Legacy `analytics_events.user_id` and `app_opens.user_id` stay nullable for
-- historical rows until the analytics backend backfills identities.

-- ---------------------------------------------------------------------------
-- Installs, identities, sessions
-- ---------------------------------------------------------------------------

-- `install_id` is a client-generated UUIDv4 held in device storage. It is
-- never an advertising ID and can be reset by the reader.
CREATE TABLE IF NOT EXISTS analytics_installs (
    install_id      uuid PRIMARY KEY,
    platform        text CHECK (platform IN ('ios','android')),
    os_version      text,
    app_version     text,
    build           text,
    locale          text,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    rotated_at      timestamptz,
    reset_at        timestamptz
);

-- The restricted join table from an analytics_id to a user_id already exists
-- as `analytics_id_map` (0014). Extend it; access is named, reason-recorded
-- and audited in analytics_identity_access_log.
ALTER TABLE analytics_id_map ADD COLUMN IF NOT EXISTS install_id uuid
    REFERENCES analytics_installs(install_id) ON DELETE SET NULL;
ALTER TABLE analytics_id_map ADD COLUMN IF NOT EXISTS actor_type text NOT NULL DEFAULT 'account';
ALTER TABLE analytics_id_map ADD COLUMN IF NOT EXISTS erased_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'analytics_id_map_actor_check') THEN
        ALTER TABLE analytics_id_map ADD CONSTRAINT analytics_id_map_actor_check
            CHECK (actor_type IN ('guest','account','under_18_private'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS analytics_id_map_install_idx
    ON analytics_id_map (install_id);

CREATE TABLE IF NOT EXISTS analytics_identity_access_log (
    id             bigserial PRIMARY KEY,
    analytics_id   uuid NOT NULL,
    actor_id       text NOT NULL REFERENCES users(id),
    reason         text NOT NULL,
    accessed_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT analytics_identity_access_reason_check CHECK (char_length(reason) >= 10)
);

CREATE TABLE IF NOT EXISTS analytics_sessions (
    session_id      uuid PRIMARY KEY,
    analytics_id    uuid NOT NULL,
    install_id      uuid REFERENCES analytics_installs(install_id) ON DELETE SET NULL,
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    client_ts       timestamptz NOT NULL,
    tz_offset_min   integer NOT NULL DEFAULT 180
                      CHECK (tz_offset_min BETWEEN -840 AND 840),
    app_version     text,
    build           text,
    platform        text CHECK (platform IN ('ios','android')),
    os_version      text,
    locale          text,
    network         text CHECK (network IN ('wifi','cellular','none','unknown')),
    actor_type      text NOT NULL DEFAULT 'guest'
                      CHECK (actor_type IN ('guest','account','under_18_private')),
    event_count     integer NOT NULL DEFAULT 0,
    capped_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT analytics_sessions_cap_check
        CHECK (ended_at IS NULL OR ended_at - started_at <= interval '24 hours')
);
CREATE INDEX IF NOT EXISTS analytics_sessions_analytics_idx
    ON analytics_sessions (analytics_id, started_at DESC);

-- ---------------------------------------------------------------------------
-- Event envelope (additive to the legacy table)
-- ---------------------------------------------------------------------------

ALTER TABLE analytics_events ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS client_event_id uuid;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS analytics_id uuid;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS session_id uuid;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS session_seq integer;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS event_family text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS client_ts timestamptz;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS received_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS server_seq bigserial;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS tz_offset_min integer;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS build text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS os_version text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS locale text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS network text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS actor_type text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS route text;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS properties_schema_version integer NOT NULL DEFAULT 1;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS dropped_properties text[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'analytics_events_family_check') THEN
        ALTER TABLE analytics_events ADD CONSTRAINT analytics_events_family_check
            CHECK (event_family IS NULL OR event_family IN ('screen_view','action','journey_step'))
            NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'analytics_events_identity_check') THEN
        ALTER TABLE analytics_events ADD CONSTRAINT analytics_events_identity_check
            CHECK (analytics_id IS NOT NULL OR user_id IS NOT NULL)
            NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'analytics_events_actor_check') THEN
        ALTER TABLE analytics_events ADD CONSTRAINT analytics_events_actor_check
            CHECK (actor_type IS NULL OR actor_type IN ('guest','account','under_18_private'))
            NOT VALID;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS analytics_events_client_event_uniq
    ON analytics_events (client_event_id) WHERE client_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS analytics_events_session_idx
    ON analytics_events (session_id, session_seq) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS analytics_events_received_brin
    ON analytics_events USING brin (received_at);
CREATE INDEX IF NOT EXISTS analytics_events_name_received_idx
    ON analytics_events (event, received_at DESC);

-- Per-event property allow-list. Unknown keys are dropped and counted.
CREATE TABLE IF NOT EXISTS analytics_event_schemas (
    event_name          text PRIMARY KEY,
    event_family        text NOT NULL
                          CHECK (event_family IN ('screen_view','action','journey_step')),
    property_schema     jsonb NOT NULL,
    properties_version  integer NOT NULL DEFAULT 1,
    banned_keys         text[] NOT NULL DEFAULT ARRAY['email','name','phone','token','latitude','longitude','query'],
    status              text NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active','deprecated','retired')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

INSERT INTO analytics_event_schemas (event_name, event_family, property_schema)
VALUES
    ('screen_view', 'screen_view',
     '{"type":"object","properties":{"route":{"type":"string"},"params":{"type":"object"}},"additionalProperties":false}'::jsonb),
    ('journey_step', 'journey_step',
     '{"type":"object","required":["journey","step","step_index","outcome"],"properties":{"journey":{"type":"string"},"step":{"type":"string"},"step_index":{"type":"integer"},"outcome":{"type":"string"},"duration_ms_bucket":{"type":"string"}},"additionalProperties":false}'::jsonb)
ON CONFLICT (event_name) DO NOTHING;

CREATE TABLE IF NOT EXISTS analytics_ingest_batches (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    analytics_id       uuid NOT NULL,
    install_id         uuid,
    consent_version    text NOT NULL,
    received_at        timestamptz NOT NULL DEFAULT now(),
    event_count        integer NOT NULL DEFAULT 0,
    accepted_count     integer NOT NULL DEFAULT 0,
    dropped_count      integer NOT NULL DEFAULT 0,
    unknown_key_count  integer NOT NULL DEFAULT 0,
    status             text NOT NULL DEFAULT 'accepted'
                         CHECK (status IN ('accepted','partial','rejected')),
    rejection_reason   text
);
CREATE INDEX IF NOT EXISTS analytics_ingest_batches_received_idx
    ON analytics_ingest_batches (received_at DESC);

-- ---------------------------------------------------------------------------
-- Consent
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS analytics_consents (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    analytics_id   uuid NOT NULL,
    scope          text NOT NULL DEFAULT 'analytics' CHECK (scope = 'analytics'),
    version        text NOT NULL,
    state          text NOT NULL CHECK (state IN ('unknown','granted','denied')),
    source         text NOT NULL CHECK (source IN ('guest_device','account')),
    recorded_at    timestamptz NOT NULL DEFAULT now(),
    withdrawn_at   timestamptz,
    UNIQUE (analytics_id, scope, version)
);
CREATE INDEX IF NOT EXISTS analytics_consents_latest_idx
    ON analytics_consents (analytics_id, scope, recorded_at DESC);

-- ---------------------------------------------------------------------------
-- Rollups (the only tables the admin reads)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS analytics_daily (
    day            date NOT NULL,
    metric         text NOT NULL,
    dimension_key  text NOT NULL DEFAULT '',
    dimension      jsonb NOT NULL DEFAULT '{}'::jsonb,
    value          numeric NOT NULL,
    suppressed     boolean NOT NULL DEFAULT false,
    sample_size    integer,
    computed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, metric, dimension_key),
    CONSTRAINT analytics_daily_suppression_check
        CHECK (NOT suppressed OR sample_size IS NULL OR sample_size < 10)
);

CREATE TABLE IF NOT EXISTS analytics_journey_daily (
    day              date NOT NULL,
    journey          text NOT NULL,
    step             text NOT NULL,
    step_index       integer NOT NULL,
    reached          integer NOT NULL DEFAULT 0,
    completed        integer NOT NULL DEFAULT 0,
    median_duration_bucket text,
    suppressed       boolean NOT NULL DEFAULT false,
    computed_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, journey, step_index, step)
);

CREATE TABLE IF NOT EXISTS analytics_retention_cohorts (
    cohort_week    date NOT NULL,
    week_index     integer NOT NULL CHECK (week_index >= 0),
    actors         integer NOT NULL DEFAULT 0,
    retained       integer NOT NULL DEFAULT 0,
    suppressed     boolean NOT NULL DEFAULT false,
    computed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cohort_week, week_index)
);

CREATE TABLE IF NOT EXISTS analytics_rollup_runs (
    id             bigserial PRIMARY KEY,
    rollup         text NOT NULL,
    day            date NOT NULL,
    status         text NOT NULL DEFAULT 'running'
                     CHECK (status IN ('running','ok','failed')),
    rows_written   integer NOT NULL DEFAULT 0,
    error          text,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    UNIQUE (rollup, day)
);

-- ---------------------------------------------------------------------------
-- Erasure, export, and service diagnostics
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS analytics_erasure_requests (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type     text NOT NULL CHECK (subject_type IN ('user','analytics_id')),
    subject_ref      text NOT NULL,
    requested_at     timestamptz NOT NULL DEFAULT now(),
    status           text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','in_progress','completed','failed')),
    target_by        timestamptz NOT NULL DEFAULT now() + interval '14 days',
    completed_at     timestamptz,
    tombstone_at     timestamptz,
    backup_cycle_until timestamptz,
    note             text
);
CREATE INDEX IF NOT EXISTS analytics_erasure_requests_due_idx
    ON analytics_erasure_requests (status, target_by);

CREATE TABLE IF NOT EXISTS analytics_erasure_events (
    id             bigserial PRIMARY KEY,
    request_id     uuid NOT NULL REFERENCES analytics_erasure_requests(id) ON DELETE CASCADE,
    step           text NOT NULL,
    status         text NOT NULL,
    detail         text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- Minimised service diagnostics: no message text and no identifiers.
CREATE TABLE IF NOT EXISTS analytics_service_diagnostics (
    id             bigserial PRIMARY KEY,
    error_code     text NOT NULL,
    route          text,
    app_version    text,
    platform       text CHECK (platform IN ('ios','android')),
    stack_hash     text,
    received_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS analytics_service_diagnostics_received_idx
    ON analytics_service_diagnostics (received_at DESC);

-- Legacy app opens become pseudonymous too.
ALTER TABLE app_opens ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE app_opens ADD COLUMN IF NOT EXISTS analytics_id uuid;
ALTER TABLE app_opens ADD COLUMN IF NOT EXISTS install_id uuid;
ALTER TABLE app_opens ADD COLUMN IF NOT EXISTS client_ts timestamptz;
