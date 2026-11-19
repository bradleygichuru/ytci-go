-- Species execution backend (ticket #369, migration range 0043-0055).
-- Owner: stream:species. Additive on top of 0043_species_contracts.sql.
--
-- Adds the reader save list and the review queue that drives the
-- translation -> language -> expert -> rights -> publish gate. Uploader
-- provenance is recorded so a rights reviewer can never decide on an asset
-- they uploaded themselves (ADR 0013 §10, roles.v1.yaml actor_separations).

-- ---------------------------------------------------------------------------
-- Reader saves (account required; guests browse but do not save)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_saves (
    user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    species_id  uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, species_id)
);
CREATE INDEX IF NOT EXISTS species_saves_user_idx
    ON species_saves (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Review queue
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS species_review_queue (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    species_id     uuid NOT NULL REFERENCES species(id) ON DELETE CASCADE,
    entity_type    text NOT NULL CHECK (entity_type IN
                     ('species','name','content','media','audio','where_to_see')),
    entity_id      uuid NOT NULL,
    language_tag   text REFERENCES content_languages(tag),
    stage          text NOT NULL CHECK (stage IN
                     ('translation','language','expert','rights','publish')),
    status         text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','approved','changes_requested','rejected')),
    assigned_to    text REFERENCES users(id) ON DELETE SET NULL,
    translator_ref text REFERENCES users(id) ON DELETE SET NULL,
    reviewer_ref   text REFERENCES users(id) ON DELETE SET NULL,
    notes          text,
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    decided_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS species_review_queue_stage_idx
    ON species_review_queue (stage, status, submitted_at);
CREATE INDEX IF NOT EXISTS species_review_queue_species_idx
    ON species_review_queue (species_id, status);
-- One live row per entity and stage. Content stages are per language; other
-- entity types carry a null language, normalised to '' for the index.
CREATE UNIQUE INDEX IF NOT EXISTS species_review_queue_entity_stage_uniq
    ON species_review_queue (entity_type, entity_id, stage, coalesce(language_tag, ''));

-- ---------------------------------------------------------------------------
-- Uploader provenance for rights separation
-- ---------------------------------------------------------------------------

ALTER TABLE species_media ADD COLUMN IF NOT EXISTS created_by text
    REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE species_audio ADD COLUMN IF NOT EXISTS created_by text
    REFERENCES users(id) ON DELETE SET NULL;
