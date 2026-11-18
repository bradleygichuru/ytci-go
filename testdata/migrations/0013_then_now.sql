-- Then & Now v1 content, authoring, and review schema.
-- Production schema is owned by the TanStack admin migrations; this mirror
-- keeps the Go integration test database and the API schema in sync.

CREATE TABLE IF NOT EXISTS heritage_sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    county TEXT NOT NULL,
    locality TEXT,
    site_type TEXT NOT NULL CHECK (site_type IN ('historical', 'cultural')),
    access_mode TEXT NOT NULL CHECK (access_mode IN ('camera_video', 'video_only', 'not_available')),
    sensitivity TEXT NOT NULL CHECK (sensitivity IN ('standard', 'sensitive', 'restricted')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'unpublished', 'archived')),
    managing_authority TEXT,
    destination_id UUID REFERENCES destinations(id) ON DELETE SET NULL,
    photography_policy TEXT,
    cultural_authority TEXT,
    consent_record TEXT,
    consent_date DATE,
    site_rules TEXT,
    safety_notes TEXT,
    short_description TEXT,
    full_description TEXT,
    sources TEXT,
    fact_check TEXT,
    public_rights_line TEXT,
    latitude NUMERIC(10, 8),
    longitude NUMERIC(11, 8),
    geofence_radius_m INTEGER NOT NULL DEFAULT 150 CHECK (geofence_radius_m BETWEEN 10 AND 5000),
    standing_point_note TEXT,
    image_key TEXT,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS heritage_sites_status_idx ON heritage_sites (status);
CREATE INDEX IF NOT EXISTS heritage_sites_county_status_idx ON heritage_sites (county, status);
CREATE INDEX IF NOT EXISTS heritage_sites_access_status_idx ON heritage_sites (access_mode, status);

CREATE TABLE IF NOT EXISTS then_now_experiences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id UUID NOT NULL REFERENCES heritage_sites(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'in_review', 'approved', 'scheduled', 'published',
        'unpublished', 'archived', 'revoked'
    )),
    live_revision_number INTEGER NOT NULL DEFAULT 0 CHECK (live_revision_number >= 0),
    open_revision_number INTEGER NOT NULL DEFAULT 1 CHECK (open_revision_number >= 1),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS then_now_experiences_site_idx ON then_now_experiences (site_id);
CREATE INDEX IF NOT EXISTS then_now_experiences_status_idx ON then_now_experiences (status);

CREATE TABLE IF NOT EXISTS then_now_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    experience_id UUID NOT NULL REFERENCES then_now_experiences(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL CHECK (revision_number >= 1),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'in_review', 'approved', 'published')),
    snapshot JSONB NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    review_notes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(review_notes) = 'array'),
    reviewer_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    submitted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    submitted_at TIMESTAMP,
    last_decision TEXT CHECK (last_decision IS NULL OR last_decision IN ('approved', 'changes', 'rejected', 'returned')),
    second_reviewer JSONB,
    second_reviewer_approved BOOLEAN NOT NULL DEFAULT false,
    client_approval JSONB,
    scheduled_at TIMESTAMP,
    schedule_note TEXT,
    published_at TIMESTAMP,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE (experience_id, revision_number)
);

CREATE INDEX IF NOT EXISTS then_now_revisions_experience_idx ON then_now_revisions (experience_id, revision_number DESC);
CREATE INDEX IF NOT EXISTS then_now_revisions_status_idx ON then_now_revisions (status);
CREATE INDEX IF NOT EXISTS then_now_revisions_reviewer_idx ON then_now_revisions (reviewer_id) WHERE status = 'in_review';

CREATE TABLE IF NOT EXISTS then_now_audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('site', 'experience', 'revision')),
    entity_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    summary TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS then_now_audit_entity_idx ON then_now_audit_events (entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS then_now_audit_created_idx ON then_now_audit_events (created_at DESC);
