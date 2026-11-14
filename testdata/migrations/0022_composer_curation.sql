-- Curator workflow for the hybrid catalog. Imported Google Places rows gain
-- an explicit review state, a linked destination, and reviewer metadata. The
-- composer read model only exposes pending/draft unverified rows; published
-- and rejected rows stop appearing as unverified candidates.

ALTER TABLE composer_unverified_candidates
    ADD COLUMN IF NOT EXISTS curation_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS destination_id UUID REFERENCES destinations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMP WITHOUT TIME ZONE,
    ADD COLUMN IF NOT EXISTS rejection_reason TEXT;

ALTER TABLE composer_unverified_candidates
    DROP CONSTRAINT IF EXISTS composer_unverified_curation_status_check;
ALTER TABLE composer_unverified_candidates
    ADD CONSTRAINT composer_unverified_curation_status_check
    CHECK (curation_status IN ('pending', 'draft', 'published', 'rejected'));

CREATE INDEX IF NOT EXISTS composer_unverified_curation_idx
    ON composer_unverified_candidates (curation_status, county_code);

-- Adopt destinations that were created before the queue existed.
UPDATE composer_unverified_candidates c
SET destination_id = d.id,
    curation_status = CASE
        WHEN d.status = 'published' AND d.verification_status = 'verified' THEN 'published'
        WHEN d.status = 'archived' THEN 'rejected'
        ELSE 'draft'
    END,
    reviewed_at = COALESCE(d.verified_at, d.updated_at)
FROM destinations d
WHERE d.google_place_id = c.place_id
  AND (c.destination_id IS DISTINCT FROM d.id OR c.curation_status = 'pending');

-- No row can be published without a recorded review. Existing published rows
-- already satisfy this; the queue sets both fields transactionally.
ALTER TABLE destinations
    DROP CONSTRAINT IF EXISTS destinations_published_requires_verification;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_published_requires_verification
    CHECK (
        status <> 'published'
        OR (verification_status = 'verified' AND verified_at IS NOT NULL)
    );

CREATE OR REPLACE VIEW composer_catalog AS
SELECT d.id AS destination_id,
    'curated'::text AS provenance,
    NULL::text AS place_id,
    d.name,
    d.slug,
    d.county_code,
    k.adm1_name AS county,
    d.locality,
    d.category,
    d.interest_tags,
    d.transport_modes,
    d.best_seasons,
    d.location,
    d.short_description,
    NULL::text AS address,
    d.visit_duration_min_minutes AS duration_min_minutes,
    d.visit_duration_max_minutes AS duration_max_minutes,
    d.fee_status,
    d.fee_currency,
    d.fee_amount,
    d.fee_basis,
    d.opening_hours_status,
    d.opening_hours,
    d.hero_image_url,
    d.hero_image_attribution,
    d.hero_image_license,
    d.hero_image_source,
    NULL::numeric AS rating,
    NULL::integer AS review_count,
    NULL::text AS price_level,
    d.verified_at,
    d.source,
    'curated_composer_ready'::text AS eligibility
   FROM destinations d
     JOIN kenya_counties k ON k.county_code = d.county_code
  WHERE d.is_curated AND d.status = 'published'::text AND d.verification_status = 'verified'::text AND d.verified_at IS NOT NULL AND d.county_code IS NOT NULL AND d.location IS NOT NULL AND (d.category = ANY (ARRAY['Activity'::text, 'Wildlife'::text, 'Hiking'::text, 'Photography'::text, 'Beach'::text, 'Culture'::text, 'Food'::text, 'Sightseeing'::text, 'Shopping'::text, 'Transport'::text, 'Rest'::text, 'Conservation'::text])) AND cardinality(d.interest_tags) > 0 AND d.short_description IS NOT NULL AND btrim(d.short_description) <> ''::text AND d.visit_duration_min_minutes IS NOT NULL AND d.visit_duration_max_minutes IS NOT NULL AND d.fee_status IS NOT NULL AND d.opening_hours_status IS NOT NULL AND d.hero_image_attribution IS NOT NULL AND btrim(d.hero_image_attribution) <> ''::text AND d.source IS NOT NULL AND btrim(d.source) <> ''::text
UNION ALL
 SELECT NULL::uuid AS destination_id,
    'google_places'::text AS provenance,
    c.place_id,
    c.name,
    NULL::text AS slug,
    c.county_code,
    k.adm1_name AS county,
    c.locality,
    c.category,
    c.interest_tags,
    ARRAY[]::text[] AS transport_modes,
    ARRAY[]::text[] AS best_seasons,
    c.location,
    NULL::text AS short_description,
    c.address,
    NULL::integer AS duration_min_minutes,
    NULL::integer AS duration_max_minutes,
    NULL::text AS fee_status,
    NULL::text AS fee_currency,
    NULL::integer AS fee_amount,
    NULL::text AS fee_basis,
        CASE
            WHEN c.opening_hours IS NOT NULL THEN 'unverified'::text
            ELSE NULL::text
        END AS opening_hours_status,
    c.opening_hours,
    c.hero_image_url,
    c.hero_image_attribution,
    NULL::text AS hero_image_license,
        CASE
            WHEN c.hero_image_url IS NOT NULL THEN 'google_places'::text
            ELSE NULL::text
        END AS hero_image_source,
    c.rating,
    c.review_count,
    c.price_level,
    NULL::timestamp without time zone AS verified_at,
    c.source,
    'unverified_google_places'::text AS eligibility
   FROM composer_unverified_candidates c
     JOIN kenya_counties k ON k.county_code = c.county_code
  WHERE c.expires_at > now() AND c.curation_status IN ('pending', 'draft') AND (c.category = ANY (ARRAY['Activity'::text, 'Wildlife'::text, 'Hiking'::text, 'Photography'::text, 'Beach'::text, 'Culture'::text, 'Food'::text, 'Sightseeing'::text, 'Shopping'::text, 'Transport'::text, 'Rest'::text, 'Conservation'::text])) AND cardinality(c.interest_tags) > 0;
