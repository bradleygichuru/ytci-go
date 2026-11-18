-- Species and language contract gate (ticket #359, migration range 0043-0055).
-- Owner: stream:species (backend #369, admin #371, mobile #370).
-- ADR 0013/0014. One base record per species; per-language variants, names,
-- audio, media, sources, rights and where-to-see evidence.

-- ---------------------------------------------------------------------------
-- Content-language registry (ADR 0014)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS content_languages (
    tag                text PRIMARY KEY,
    english_name       text NOT NULL,
    native_name        text,
    coverage_tier      text NOT NULL DEFAULT 'names'
                         CHECK (coverage_tier IN ('full','summary','names','audio')),
    writing_direction  text NOT NULL DEFAULT 'ltr' CHECK (writing_direction IN ('ltr','rtl')),
    launch_phase       integer NOT NULL DEFAULT 2,
    status             text NOT NULL DEFAULT 'planned'
                         CHECK (status IN ('planned','active','retired')),
    created_at         timestamptz NOT NULL DEFAULT now()
);

INSERT INTO content_languages (tag, english_name, native_name, coverage_tier, launch_phase, status)
VALUES
    ('en', 'English',   'English',   'full', 1, 'active'),
    ('sw', 'Swahili',   'Kiswahili', 'full', 1, 'active'),
    ('ki', 'Kikuyu',    'Gĩkũyũ',    'names', 2, 'active'),
    ('luo','Dholuo',    'Dholuo',    'names', 2, 'active'),
    ('kam','Kamba',     'Kikamba',   'names', 2, 'active'),
    ('mas','Maasai',    'Maa',       'names', 2, 'active'),
    ('so', 'Somali',    'Soomaali',  'names', 2, 'active'),
    ('luy','Luhya',     'Luluhya',   'names', 3, 'planned'),
    ('kln','Kalenjin',  'Kalenjin',  'names', 3, 'planned'),
    ('guz','Gusii',     'Ekegusii',  'names', 3, 'planned'),
    ('mer','Meru',      'Kĩmĩrũ',    'names', 3, 'planned'),
    ('nyf','Giryama',   'Kigiryama', 'names', 3, 'planned'),
    ('fr', 'French',    'Français',  'summary', 3, 'planned'),
    ('de', 'German',    'Deutsch',   'summary', 3, 'planned'),
    ('zh-Hans', 'Chinese (Simplified)', '简体中文', 'summary', 3, 'planned'),
    ('ar', 'Arabic',    'العربية',   'summary', 3, 'planned')
ON CONFLICT (tag) DO NOTHING;

ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS content_language text
    REFERENCES content_languages(tag) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- Base species record
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                text NOT NULL UNIQUE,
    scientific_name     text NOT NULL,
    authorship          text,
    taxonomic_rank      text NOT NULL DEFAULT 'species',
    taxonomic_status    text NOT NULL DEFAULT 'accepted'
                          CHECK (taxonomic_status IN ('accepted','synonym','provisional','uncertain')),
    accepted_species_id uuid REFERENCES species(id) ON DELETE SET NULL,
    gbif_key            text,
    iucn_taxon_id       text,
    wikidata_qid        text,
    iucn_category       char(2) CHECK (iucn_category IN ('CR','EN','VU','NT','LC','DD','NE')),
    iucn_assessment_id  text,
    red_list_version    text,
    status_as_of        date,
    content_version     integer NOT NULL DEFAULT 1,
    publication_state   text NOT NULL DEFAULT 'draft'
                          CHECK (publication_state IN
                          ('draft','in_review','published','unpublished','tombstoned')),
    created_by          text REFERENCES users(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT species_status_provenance_check CHECK (
        iucn_category IS NULL OR (iucn_assessment_id IS NOT NULL AND status_as_of IS NOT NULL)
    )
);
CREATE INDEX IF NOT EXISTS species_state_idx ON species (publication_state, scientific_name);
CREATE INDEX IF NOT EXISTS species_iucn_idx ON species (iucn_category) WHERE iucn_category IS NOT NULL;
CREATE INDEX IF NOT EXISTS species_gbif_idx ON species (gbif_key) WHERE gbif_key IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Sources and rights
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_sources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id      uuid REFERENCES species(id) ON DELETE CASCADE,
    publisher       text NOT NULL,
    citation        text NOT NULL,
    url             text,
    accessed_at     date NOT NULL,
    licence_code    text NOT NULL,
    licence_url     text,
    permitted_use   text NOT NULL CHECK (permitted_use IN
                      ('taxonomy_only','occurrence','prose','media','audio','link_only')),
    attribution     text NOT NULL,
    snapshot_hash   text,
    review_status   text NOT NULL DEFAULT 'reviewed'
                      CHECK (review_status IN ('missing','draft','reviewed','stale')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- IUCN narratives, threats and range maps are never stored without
    -- written permission; category + assessment metadata is the allowed shape.
    CONSTRAINT species_sources_iucn_use_check CHECK (
        publisher <> 'IUCN' OR permitted_use = 'taxonomy_only'
    )
);
CREATE INDEX IF NOT EXISTS species_sources_species_idx ON species_sources (species_id);

CREATE TABLE IF NOT EXISTS species_rights (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type    text NOT NULL CHECK (subject_type IN ('media','audio')),
    subject_id      uuid NOT NULL,
    rights_holder   text NOT NULL,
    licence_code    text NOT NULL,
    licence_url     text,
    permitted_use   text NOT NULL,
    attribution     text NOT NULL,
    consent_record  text,
    expires_at      timestamptz,
    withdrawn_at    timestamptz,
    reviewed_by     text REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (subject_type, subject_id)
);

-- ---------------------------------------------------------------------------
-- Names and prose variants
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_names (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id      uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    language_tag    text NOT NULL REFERENCES content_languages(tag),
    script          text NOT NULL DEFAULT 'Latn',
    name            text NOT NULL,
    name_type       text NOT NULL DEFAULT 'common'
                      CHECK (name_type IN ('common','local','scientific','alternate')),
    region          text,
    county_gid      integer REFERENCES kenya_counties(gid) ON DELETE SET NULL,
    source_id       uuid REFERENCES species_sources(id) ON DELETE SET NULL,
    reviewer_id     text REFERENCES users(id) ON DELETE SET NULL,
    review_status   text NOT NULL DEFAULT 'draft'
                      CHECK (review_status IN ('missing','draft','reviewed','stale')),
    preferred       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT species_names_county_region_check
        CHECK (county_gid IS NULL OR region IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS species_names_species_lang_idx
    ON species_names (species_id, language_tag);
CREATE UNIQUE INDEX IF NOT EXISTS species_names_preferred_uniq
    ON species_names (species_id, language_tag, md5(coalesce(region, '')))
    WHERE preferred;

CREATE TABLE IF NOT EXISTS species_content (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id        uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    language_tag      text NOT NULL REFERENCES content_languages(tag),
    coverage_tier     text NOT NULL DEFAULT 'summary'
                        CHECK (coverage_tier IN ('full','summary','names','audio')),
    summary           text,
    description       text,
    habitat           text,
    range_text        text,
    where_to_see_text text,
    conservation_text text,
    field_status      jsonb NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(field_status) = 'object'),
    source_version    integer NOT NULL DEFAULT 1,
    translator_id     text REFERENCES users(id) ON DELETE SET NULL,
    reviewer_id       text REFERENCES users(id) ON DELETE SET NULL,
    review_status     text NOT NULL DEFAULT 'draft'
                        CHECK (review_status IN ('missing','draft','reviewed','stale')),
    stale_reason      text,
    published_at      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (species_id, language_tag),
    -- Translator and reviewer must differ (ISO 17100-style separation).
    CONSTRAINT species_content_reviewer_separation_check
        CHECK (translator_id IS NULL OR reviewer_id IS NULL OR translator_id <> reviewer_id)
);
CREATE INDEX IF NOT EXISTS species_content_lang_idx ON species_content (language_tag, review_status);

-- ---------------------------------------------------------------------------
-- Media and audio
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_media (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id          uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    media_kind          text NOT NULL DEFAULT 'image'
                          CHECK (media_kind IN ('image','video','map')),
    storage_key         text NOT NULL,
    width               integer,
    height              integer,
    rights_id           uuid REFERENCES species_rights(id) ON DELETE SET NULL,
    credit              text NOT NULL,
    rights_holder       text NOT NULL,
    licence_code        text NOT NULL,
    licence_url         text,
    sensitive_location  boolean NOT NULL DEFAULT false,
    review_status       text NOT NULL DEFAULT 'draft'
                          CHECK (review_status IN ('missing','draft','reviewed','stale')),
    published_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- No hotlinking: storage_key is a managed object key.
    CONSTRAINT species_media_no_external_check CHECK (storage_key NOT LIKE 'http%')
);

CREATE TABLE IF NOT EXISTS species_media_alt (
    media_id        uuid NOT NULL REFERENCES species_media(id) ON DELETE CASCADE,
    language_tag    text NOT NULL REFERENCES content_languages(tag),
    alt_text        text NOT NULL,
    review_status   text NOT NULL DEFAULT 'draft'
                      CHECK (review_status IN ('missing','draft','reviewed','stale')),
    PRIMARY KEY (media_id, language_tag)
);

CREATE TABLE IF NOT EXISTS species_audio (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id        uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    language_tag      text NOT NULL REFERENCES content_languages(tag),
    storage_key       text NOT NULL,
    narrator          text NOT NULL,
    transcript        text NOT NULL,
    duration_seconds  integer NOT NULL CHECK (duration_seconds > 0),
    rights_id         uuid REFERENCES species_rights(id) ON DELETE SET NULL,
    review_status     text NOT NULL DEFAULT 'draft'
                        CHECK (review_status IN ('missing','draft','reviewed','stale')),
    published_at      timestamptz,
    stale_at          timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (species_id, language_tag, storage_key)
);

-- ---------------------------------------------------------------------------
-- Where-to-see evidence and links
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_where_to_see (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id      uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    destination_slug text REFERENCES destinations(slug) ON DELETE SET NULL,
    county_gid      integer REFERENCES kenya_counties(gid) ON DELETE SET NULL,
    protected_area  text,
    evidence_type   text NOT NULL CHECK (evidence_type IN
                      ('survey','occurrence_record','expert_observation','published_report')),
    confidence      text NOT NULL DEFAULT 'medium'
                      CHECK (confidence IN ('low','medium','high')),
    source_id       uuid REFERENCES species_sources(id) ON DELETE SET NULL,
    review_status   text NOT NULL DEFAULT 'draft'
                      CHECK (review_status IN ('missing','draft','reviewed','stale')),
    notes           text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT species_where_to_see_target_check
        CHECK (destination_slug IS NOT NULL OR county_gid IS NOT NULL OR protected_area IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS species_where_to_see_species_idx
    ON species_where_to_see (species_id, review_status);
CREATE INDEX IF NOT EXISTS species_where_to_see_destination_idx
    ON species_where_to_see (destination_slug);

CREATE TABLE IF NOT EXISTS species_links (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id   uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    target_type  text NOT NULL CHECK (target_type IN ('destination','course','audio_story','event')),
    target_id    text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (species_id, target_type, target_id)
);

-- Append-only review evidence for every publishable entity.
CREATE TABLE IF NOT EXISTS species_reviews (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type    text NOT NULL CHECK (entity_type IN
                     ('species','name','content','media','audio','where_to_see')),
    entity_id      uuid NOT NULL,
    stage          text NOT NULL CHECK (stage IN
                     ('translation','language','expert','rights','publish')),
    reviewer_id    text NOT NULL REFERENCES users(id),
    decision       text NOT NULL CHECK (decision IN ('approved','changes_requested','rejected')),
    notes          text,
    source_version integer,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS species_reviews_entity_idx
    ON species_reviews (entity_type, entity_id, created_at DESC);
