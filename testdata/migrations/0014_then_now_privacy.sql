-- Then & Now privacy controls: consent versioning, pseudonymous analytics,
-- and anonymous heritage counts.
--
-- The privacy requirements are in docs/privacy/then-now-recognition.md.
-- Recognition stays dark until the enablement gate is signed.

ALTER TABLE user_profiles
    ADD COLUMN IF NOT EXISTS consent_version TEXT,
    ADD COLUMN IF NOT EXISTS consent_recognition BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS consent_location_verification BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS consent_public_profile BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS consent_leaderboard BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS consent_analytics BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS consent_withdrawn_at TIMESTAMP;

CREATE TABLE IF NOT EXISTS consent_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    version TEXT NOT NULL,
    location BOOLEAN NOT NULL DEFAULT false,
    camera BOOLEAN NOT NULL DEFAULT false,
    notifications BOOLEAN NOT NULL DEFAULT false,
    ugc BOOLEAN NOT NULL DEFAULT false,
    recognition BOOLEAN NOT NULL DEFAULT false,
    location_verification BOOLEAN NOT NULL DEFAULT false,
    public_profile BOOLEAN NOT NULL DEFAULT false,
    leaderboard BOOLEAN NOT NULL DEFAULT false,
    analytics BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS consent_records_user_idx ON consent_records (user_id, created_at DESC);

-- Analytics move from the account user ID to a random analytics ID. Old rows
-- keep user_id; new rows use analytics_id. The mapping is restricted and is
-- deleted on account deletion, leaving the analytics rows pseudonymous.
ALTER TABLE app_opens ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE app_opens ADD COLUMN IF NOT EXISTS analytics_id UUID;
CREATE INDEX IF NOT EXISTS app_opens_analytics_idx ON app_opens (analytics_id, opened_at DESC);

ALTER TABLE analytics_events ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS analytics_id UUID;
CREATE INDEX IF NOT EXISTS analytics_events_analytics_idx ON analytics_events (analytics_id, created_at DESC);

CREATE TABLE IF NOT EXISTS analytics_id_map (
    analytics_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Anonymous heritage counts. Sensitive sites are stored with site_id NULL and
-- scope='sensitive'; reads suppress counts below 5.
CREATE TABLE IF NOT EXISTS anonymous_heritage_counts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    day DATE NOT NULL DEFAULT CURRENT_DATE,
    scope TEXT NOT NULL CHECK (scope IN ('site', 'sensitive')),
    site_id UUID REFERENCES heritage_sites(id) ON DELETE SET NULL,
    event TEXT NOT NULL CHECK (event IN (
        'portal_opened', 'video_played', 'visit_marked', 'verified_visit'
    )),
    count BIGINT NOT NULL DEFAULT 0 CHECK (count >= 0),
    CONSTRAINT anonymous_heritage_counts_unique
        UNIQUE NULLS NOT DISTINCT (day, scope, site_id, event)
);

CREATE INDEX IF NOT EXISTS anonymous_heritage_counts_day_idx
    ON anonymous_heritage_counts (day DESC, event);
