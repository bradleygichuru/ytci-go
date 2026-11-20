-- Repository data foundation (ticket #7) — Go test-schema mirror of
-- ytci-tanstack/src/db/migrations/0036_repository.sql.
--
-- Additive only: five new tables, one immutable helper function and indexes.
-- Handlers for these tables land in tickets #8 (reads/search) and #9
-- (submissions/uploads/admin CRUD); this file only keeps the integration test
-- schema aligned with the admin Drizzle migration.

-- ---------------------------------------------------------------------------
-- Helper: array_to_string is STABLE, not IMMUTABLE, so the generated column
-- cannot call it directly.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION repository_text_array(text[])
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT coalesce(array_to_string($1, ' '), '')
$$;

-- ---------------------------------------------------------------------------
-- Media owner type (the check was created in 0002 and widened in 0013)
-- ---------------------------------------------------------------------------

ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_entity_type_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_entity_type_check
  CHECK (entity_type IN (
    'destination', 'story', 'event', 'course', 'conservation_activity',
    'user', 'audio_story', 'unlinked', 'repository_entry'
  ));

-- ---------------------------------------------------------------------------
-- Catalogue
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS repository_entries (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                text NOT NULL
                          CHECK (kind IN ('competition_entry', 'innovation', 'conservation_resource', 'experience')),
    slug                text NOT NULL,
    title               text NOT NULL,
    summary             text NOT NULL,
    body_md             text NOT NULL DEFAULT '',
    county              text,
    category            text,
    tags                text[] NOT NULL DEFAULT '{}'::text[],
    author_name         text,
    author_email        text,
    author_phone        text,
    author_age_band     text,
    author_org          text,
    team_members        jsonb,
    competition_name    text,
    competition_category text,
    cover_media_id      uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    document_media_id   uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    external_url        text,
    embed_url           text,
    embed_provider      text,
    status              text NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft', 'submitted', 'approved', 'published', 'rejected', 'archived')),
    source              text NOT NULL DEFAULT 'admin'
                          CHECK (source IN ('admin', 'public')),
    featured            boolean NOT NULL DEFAULT false,
    moderation_note     text,
    reviewed_by         text REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at         timestamp,
    consent_at          timestamp,
    consent_version     text,
    reference_code      text,
    published_at        timestamp,
    search_vector       tsvector GENERATED ALWAYS AS (
                          to_tsvector(
                            'english',
                            coalesce(title, '') || ' ' ||
                            coalesce(summary, '') || ' ' ||
                            coalesce(body_md, '') || ' ' ||
                            repository_text_array(tags)
                          )
                        ) STORED,
    created_at          timestamp NOT NULL DEFAULT now(),
    updated_at          timestamp NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS repository_entries_slug_idx
    ON repository_entries (slug);

CREATE UNIQUE INDEX IF NOT EXISTS repository_entries_reference_code_idx
    ON repository_entries (reference_code);

CREATE INDEX IF NOT EXISTS repository_entries_status_published_at_idx
    ON repository_entries (status, published_at DESC);

CREATE INDEX IF NOT EXISTS repository_entries_kind_county_idx
    ON repository_entries (kind, county);

CREATE INDEX IF NOT EXISTS repository_entries_kind_category_idx
    ON repository_entries (kind, category);

CREATE INDEX IF NOT EXISTS repository_entries_search_idx
    ON repository_entries USING gin (search_vector);

-- ---------------------------------------------------------------------------
-- Gallery join
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS repository_entry_media (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entry_id    uuid NOT NULL REFERENCES repository_entries(id) ON DELETE CASCADE,
    media_id    uuid NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    sort_order  integer NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS repository_entry_media_entry_media_idx
    ON repository_entry_media (entry_id, media_id);

CREATE INDEX IF NOT EXISTS repository_entry_media_entry_sort_idx
    ON repository_entry_media (entry_id, sort_order);

-- ---------------------------------------------------------------------------
-- Anonymous upload intents
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS repository_upload_intents (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    object_key          text NOT NULL,
    content_type        text NOT NULL,
    expected_size_bytes bigint NOT NULL CHECK (expected_size_bytes > 0),
    kind                text NOT NULL CHECK (kind IN ('image', 'document')),
    created_at          timestamp NOT NULL DEFAULT now(),
    expires_at          timestamp NOT NULL DEFAULT (now() + interval '30 minutes'),
    consumed_at         timestamp
);

CREATE UNIQUE INDEX IF NOT EXISTS repository_upload_intents_object_key_idx
    ON repository_upload_intents (object_key);

CREATE INDEX IF NOT EXISTS repository_upload_intents_expires_at_idx
    ON repository_upload_intents (expires_at);

-- ---------------------------------------------------------------------------
-- Settings singleton
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS repository_settings (
    id                  text PRIMARY KEY DEFAULT 'default'
                          CHECK (id = 'default'),
    submissions_open    boolean NOT NULL DEFAULT false,
    submission_intro    text,
    terms_version       text NOT NULL DEFAULT '2026-11',
    updated_at          timestamp NOT NULL DEFAULT now()
);

INSERT INTO repository_settings (id)
VALUES ('default')
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Fixed category taxonomy per kind
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS repository_categories (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        text NOT NULL
                  CHECK (kind IN ('competition_entry', 'innovation', 'conservation_resource', 'experience')),
    name        text NOT NULL,
    slug        text NOT NULL,
    sort_order  integer NOT NULL DEFAULT 0,
    created_at  timestamp NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS repository_categories_kind_slug_idx
    ON repository_categories (kind, slug);

CREATE INDEX IF NOT EXISTS repository_categories_kind_sort_idx
    ON repository_categories (kind, sort_order);

INSERT INTO repository_categories (kind, name, slug, sort_order) VALUES
    ('competition_entry', 'Photography', 'photography', 0),
    ('competition_entry', 'Video', 'video', 1),
    ('competition_entry', 'Storytelling', 'storytelling', 2),
    ('competition_entry', 'Digital Campaign', 'digital-campaign', 3),
    ('competition_entry', 'Innovation Pitch', 'innovation-pitch', 4),
    ('competition_entry', 'Other', 'other', 5),
    ('innovation', 'Tourism Tech', 'tourism-tech', 0),
    ('innovation', 'Sustainable Travel', 'sustainable-travel', 1),
    ('innovation', 'Community Impact', 'community-impact', 2),
    ('innovation', 'Creative Enterprise', 'creative-enterprise', 3),
    ('innovation', 'Other', 'other', 4),
    ('conservation_resource', 'Sustainable Tourism', 'sustainable-tourism', 0),
    ('conservation_resource', 'Digital Tourism', 'digital-tourism', 1),
    ('conservation_resource', 'Youth Investment', 'youth-investment', 2),
    ('conservation_resource', 'Conservation Practice', 'conservation-practice', 3),
    ('conservation_resource', 'County Guide', 'county-guide', 4),
    ('conservation_resource', 'Other', 'other', 5),
    ('experience', 'National Parks', 'national-parks', 0),
    ('experience', 'Coast', 'coast', 1),
    ('experience', 'Culture & Heritage', 'culture-and-heritage', 2),
    ('experience', 'Urban', 'urban', 3),
    ('experience', 'Adventure', 'adventure', 4),
    ('experience', 'Other', 'other', 5)
ON CONFLICT (kind, slug) DO NOTHING;
