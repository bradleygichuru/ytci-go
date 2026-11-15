-- Alerts domain: official warnings/advisories, contextual fan-out, and program updates.
-- Additive only; the existing campaign push path is untouched.

CREATE TABLE IF NOT EXISTS alert_sources (
    id                      text PRIMARY KEY,
    name                    text NOT NULL,
    kind                    text NOT NULL CHECK (kind IN ('cap_rss','json','manual')),
    feed_url                text,
    homepage_url            text,
    trust_level             text NOT NULL CHECK (trust_level IN ('official','official_foreign','ytci')),
    poll_interval_seconds   integer NOT NULL DEFAULT 300,
    enabled                 boolean NOT NULL DEFAULT true,
    push_enabled            boolean NOT NULL DEFAULT false,
    last_checked_at         timestamptz,
    last_success_at         timestamptz,
    last_item_at            timestamptz,
    consecutive_failures    integer NOT NULL DEFAULT 0,
    last_http_status        integer,
    last_error              text,
    last_etag               text,
    last_modified           text,
    last_build_date         text,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alert_source_runs (
    id              bigserial PRIMARY KEY,
    source_id       text NOT NULL REFERENCES alert_sources(id) ON DELETE CASCADE,
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    status          text NOT NULL CHECK (status IN ('running','ok','not_modified','http_error','parse_error','timeout','partial')),
    http_status     integer,
    items_seen      integer NOT NULL DEFAULT 0,
    items_new       integer NOT NULL DEFAULT 0,
    items_updated   integer NOT NULL DEFAULT 0,
    bytes_received  bigint,
    duration_ms     integer,
    error           text
);
CREATE INDEX IF NOT EXISTS alert_source_runs_source_idx ON alert_source_runs (source_id, started_at DESC);

CREATE TABLE IF NOT EXISTS alerts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id               text NOT NULL REFERENCES alert_sources(id),
    source_identifier       text NOT NULL,
    source_url              text,
    msg_type                text NOT NULL DEFAULT 'Alert'
                              CHECK (msg_type IN ('Alert','Update','Cancel','Ack','Error')),
    status                  text NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active','cancelled','expired','superseded')),
    category                text NOT NULL DEFAULT 'Other',
    categories              text[] NOT NULL DEFAULT '{}',
    event                   text NOT NULL,
    event_codes             jsonb,
    severity                text NOT NULL
                              CHECK (severity IN ('Extreme','Severe','Moderate','Minor','Unknown')),
    urgency                 text NOT NULL
                              CHECK (urgency IN ('Immediate','Expected','Future','Past','Unknown')),
    certainty               text NOT NULL
                              CHECK (certainty IN ('Observed','Likely','Possible','Unlikely','Unknown')),
    language                text NOT NULL DEFAULT 'en',
    headline                text,
    description             text,
    instruction              text,
    web_url                 text,
    contact                 text,
    sender                  text,
    sender_name             text,
    sent_at                 timestamptz NOT NULL,
    effective_at            timestamptz,
    onset_at                timestamptz,
    expires_at              timestamptz,
    received_at             timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    content_hash            text NOT NULL,
    raw_area_desc           text,
    mapping_method          text CHECK (mapping_method IN
                              ('polygon','area_desc','region','national','manual','unmapped')),
    mapping_review          boolean NOT NULL DEFAULT false,
    cap_references          text[] NOT NULL DEFAULT '{}',
    is_test                 boolean NOT NULL DEFAULT false,
    is_public               boolean NOT NULL DEFAULT true,
    cancelled_at            timestamptz,
    UNIQUE (source_id, source_identifier)
);
CREATE INDEX IF NOT EXISTS alerts_active_idx ON alerts (severity, onset_at) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS alerts_source_sent_idx ON alerts (source_id, sent_at DESC);
CREATE INDEX IF NOT EXISTS alerts_expiry_idx ON alerts (expires_at) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS alerts_mapping_review_idx ON alerts (received_at) WHERE mapping_review;

CREATE TABLE IF NOT EXISTS alert_source_messages (
    source_id         text NOT NULL REFERENCES alert_sources(id) ON DELETE CASCADE,
    source_identifier text NOT NULL,
    alert_id          uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    content_hash      text NOT NULL,
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, source_identifier)
);
CREATE INDEX IF NOT EXISTS alert_source_messages_alert_idx ON alert_source_messages (alert_id);

CREATE TABLE IF NOT EXISTS alert_areas (
    alert_id      uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    county_gid    integer NOT NULL REFERENCES kenya_counties(gid),
    method        text NOT NULL CHECK (method IN ('polygon','area_desc','region','national','manual')),
    overlap_pct   numeric(6,3),
    overlap_km2   numeric(10,1),
    is_primary    boolean NOT NULL DEFAULT false,
    PRIMARY KEY (alert_id, county_gid)
);
CREATE INDEX IF NOT EXISTS alert_areas_county_idx ON alert_areas (county_gid, alert_id);

CREATE TABLE IF NOT EXISTS alert_updates (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id          uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    version           integer NOT NULL,
    source_identifier text NOT NULL,
    msg_type          text NOT NULL,
    content_hash      text NOT NULL,
    changed_fields    text[] NOT NULL DEFAULT '{}',
    snapshot          jsonb NOT NULL,
    source_sent_at    timestamptz,
    received_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (alert_id, version)
);
CREATE INDEX IF NOT EXISTS alert_updates_alert_idx ON alert_updates (alert_id, version DESC);

CREATE TABLE IF NOT EXISTS alert_preferences (
    user_id                   text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled                   boolean NOT NULL DEFAULT true,
    min_severity              text NOT NULL DEFAULT 'Severe'
                                CHECK (min_severity IN ('Extreme','Severe','Moderate','Minor')),
    categories                text[] NOT NULL DEFAULT ARRAY['Met','Safety','Security'],
    quiet_hours_enabled       boolean NOT NULL DEFAULT true,
    quiet_hours_start         time NOT NULL DEFAULT '22:00',
    quiet_hours_end           time NOT NULL DEFAULT '06:00',
    timezone                  text NOT NULL DEFAULT 'Africa/Nairobi',
    critical_override         boolean NOT NULL DEFAULT true,
    home_county_gid           integer REFERENCES kenya_counties(gid),
    watch_county_gids         integer[] NOT NULL DEFAULT '{}',
    follow_current_location   boolean NOT NULL DEFAULT true,
    follow_itineraries        boolean NOT NULL DEFAULT false,
    follow_saved_destinations boolean NOT NULL DEFAULT false,
    language                  text NOT NULL DEFAULT 'en',
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS alert_preferences_watch_idx ON alert_preferences USING GIN (watch_county_gids);
CREATE INDEX IF NOT EXISTS alert_preferences_home_idx ON alert_preferences (home_county_gid);

CREATE TABLE IF NOT EXISTS user_alert_location (
    user_id      text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    county_gid   integer NOT NULL REFERENCES kenya_counties(gid),
    source       text NOT NULL DEFAULT 'app' CHECK (source IN ('app','manual')),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_alert_location_county_idx ON user_alert_location (county_gid);

CREATE TABLE IF NOT EXISTS alert_read_state (
    user_id       text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alert_id      uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    read_at       timestamptz,
    dismissed_at  timestamptz,
    PRIMARY KEY (user_id, alert_id)
);
CREATE INDEX IF NOT EXISTS alert_read_state_user_idx ON alert_read_state (user_id, read_at);

CREATE TABLE IF NOT EXISTS alert_fanout_jobs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id          uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    reason            text NOT NULL CHECK (reason IN ('new','update','cancel')),
    content_hash      text NOT NULL,
    status            text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','running','done','dead')),
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    recipient_count   integer,
    last_error        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    started_at        timestamptz,
    finished_at       timestamptz,
    UNIQUE (alert_id, reason, content_hash)
);
CREATE INDEX IF NOT EXISTS alert_fanout_jobs_due_idx ON alert_fanout_jobs (status, next_attempt_at);

CREATE TABLE IF NOT EXISTS alert_deliveries (
    id                  bigserial PRIMARY KEY,
    alert_id            uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    user_id             text NOT NULL,
    push_token_id       uuid NOT NULL REFERENCES push_tokens(id) ON DELETE CASCADE,
    county_gid          integer,
    reason              text NOT NULL CHECK (reason IN ('home','location','itinerary','saved','watch','national','manual')),
    status              text NOT NULL DEFAULT 'queued'
                          CHECK (status IN ('queued','sending','sent','failed',
                                            'skipped_quiet_hours','skipped_expired',
                                            'skipped_preference','cancelled','unknown')),
    deliver_after       timestamptz NOT NULL DEFAULT now(),
    attempts            integer NOT NULL DEFAULT 0,
    expo_ticket_id      text,
    expo_receipt_status text,
    error_code          text,
    error_message       text,
    sent_at             timestamptz,
    receipt_checked_at  timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (alert_id, push_token_id)
);
CREATE INDEX IF NOT EXISTS alert_deliveries_due_idx ON alert_deliveries (status, deliver_after);
CREATE INDEX IF NOT EXISTS alert_deliveries_alert_idx ON alert_deliveries (alert_id);
CREATE INDEX IF NOT EXISTS alert_deliveries_user_idx ON alert_deliveries (user_id);
CREATE INDEX IF NOT EXISTS alert_deliveries_ticket_idx ON alert_deliveries (expo_ticket_id)
    WHERE expo_ticket_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS alert_deliveries_receipt_due_idx ON alert_deliveries (sent_at)
    WHERE expo_ticket_id IS NOT NULL AND receipt_checked_at IS NULL;

CREATE TABLE IF NOT EXISTS alert_worker_heartbeat (
    worker       text PRIMARY KEY,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    version      text,
    metadata     jsonb
);

CREATE TABLE IF NOT EXISTS program_updates (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title             text NOT NULL,
    body              text NOT NULL,
    priority          text NOT NULL DEFAULT 'Normal' CHECK (priority IN ('Normal','Important')),
    status            text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft','scheduled','published','expired','archived')),
    county_gids       integer[] NOT NULL DEFAULT '{}',
    categories        text[] NOT NULL DEFAULT '{}',
    image_url         text,
    link_url          text,
    published_at      timestamptz,
    expires_at        timestamptz,
    created_by        text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS program_updates_status_idx ON program_updates (status, published_at DESC);

CREATE TABLE IF NOT EXISTS program_update_read_state (
    user_id           text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    program_update_id uuid NOT NULL REFERENCES program_updates(id) ON DELETE CASCADE,
    read_at           timestamptz,
    PRIMARY KEY (user_id, program_update_id)
);

ALTER TABLE push_tokens ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE push_tokens ADD COLUMN IF NOT EXISTS installation_id text;
ALTER TABLE push_tokens ADD COLUMN IF NOT EXISTS last_registered_at timestamptz;
ALTER TABLE push_tokens ADD COLUMN IF NOT EXISTS deactivated_at timestamptz;
ALTER TABLE push_tokens ADD COLUMN IF NOT EXISTS deactivation_reason text;

INSERT INTO alert_sources
    (id, name, kind, feed_url, homepage_url, trust_level, poll_interval_seconds, enabled, push_enabled)
VALUES
    ('kmd',  'Kenya Meteorological Department', 'cap_rss',
     'https://meteo.go.ke/api/cap/rss.xml',
     'https://meteo.go.ke/weather-warnings/', 'official', 300, true, true),
    ('fcdo', 'UK FCDO travel advice for Kenya', 'json',
     'https://www.gov.uk/api/content/foreign-travel-advice/kenya',
     'https://www.gov.uk/foreign-travel-advice/kenya', 'official_foreign', 900, true, false),
    ('ytci', 'YTCI announcements', 'manual', NULL, NULL, 'ytci', 0, false, false)
ON CONFLICT (id) DO NOTHING;
