-- Composer catalog read model and county coverage.
--
-- `composer_catalog` is the only surface the trip composer should read.
-- Curated rows must satisfy the composer-ready contract; Google Places rows
-- live in `composer_unverified_candidates` and never carry fees, duration,
-- safety, accessibility, or seasonality claims.

ALTER TABLE destinations ADD COLUMN IF NOT EXISTS transport_modes TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS best_seasons TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_transport_modes_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_transport_modes_check
    CHECK (transport_modes <@ ARRAY[
        'car', '4x4', 'flight', 'public_transport', 'boat', 'walking', 'cycling'
    ]::TEXT[]);

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_best_seasons_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_best_seasons_check
    CHECK (best_seasons <@ ARRAY['dry', 'green', 'migration', 'year_round']::TEXT[]);

CREATE INDEX IF NOT EXISTS destinations_transport_modes_idx
    ON destinations USING GIN (transport_modes);
CREATE INDEX IF NOT EXISTS destinations_best_seasons_idx
    ON destinations USING GIN (best_seasons);

-- Unverified Google Places candidates are a separate, expiring read model.
-- Discovery (#218) populates this table; it never creates destination drafts.
CREATE TABLE IF NOT EXISTS composer_unverified_candidates (
    place_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT,
    county_code TEXT NOT NULL
        REFERENCES kenya_counties(county_code) ON UPDATE CASCADE,
    locality TEXT,
    category TEXT NOT NULL
        CHECK (category IN (
            'Activity', 'Wildlife', 'Hiking', 'Photography', 'Beach', 'Culture',
            'Food', 'Sightseeing', 'Shopping', 'Transport', 'Rest', 'Conservation'
        )),
    interest_tags TEXT[] NOT NULL
        CHECK (
            cardinality(interest_tags) > 0
            AND interest_tags <@ ARRAY[
                'Activity', 'Wildlife', 'Hiking', 'Photography', 'Beach', 'Culture',
                'Food', 'Sightseeing', 'Shopping', 'Transport', 'Rest', 'Conservation'
            ]::TEXT[]
        ),
    location geometry(Point, 4326) NOT NULL,
    rating NUMERIC(2, 1) CHECK (rating IS NULL OR (rating >= 0 AND rating <= 5)),
    review_count INTEGER CHECK (review_count IS NULL OR review_count >= 0),
    price_level TEXT,
    hero_image_url TEXT,
    hero_image_attribution TEXT,
    opening_hours JSONB,
    google_types TEXT[] NOT NULL DEFAULT '{}',
    source TEXT NOT NULL DEFAULT 'google_places' CHECK (source = 'google_places'),
    fetched_at TIMESTAMP NOT NULL DEFAULT now(),
    expires_at TIMESTAMP NOT NULL DEFAULT now() + interval '30 days',
    CHECK (
        hero_image_url IS NULL
        OR (hero_image_attribution IS NOT NULL AND btrim(hero_image_attribution) <> '')
    )
);

CREATE INDEX IF NOT EXISTS composer_unverified_county_idx
    ON composer_unverified_candidates (county_code);
CREATE INDEX IF NOT EXISTS composer_unverified_location_idx
    ON composer_unverified_candidates USING GIST (location);
CREATE INDEX IF NOT EXISTS composer_unverified_interest_idx
    ON composer_unverified_candidates USING GIN (interest_tags);
CREATE INDEX IF NOT EXISTS composer_unverified_expiry_idx
    ON composer_unverified_candidates (expires_at);

CREATE OR REPLACE VIEW composer_catalog AS
SELECT
    d.id AS destination_id,
    'curated'::TEXT AS provenance,
    NULL::TEXT AS place_id,
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
    NULL::TEXT AS address,
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
    NULL::NUMERIC AS rating,
    NULL::INTEGER AS review_count,
    NULL::TEXT AS price_level,
    d.verified_at,
    d.source,
    'curated_composer_ready'::TEXT AS eligibility
FROM destinations d
JOIN kenya_counties k ON k.county_code = d.county_code
WHERE d.is_curated
  AND d.status = 'published'
  AND d.verification_status = 'verified'
  AND d.verified_at IS NOT NULL
  AND d.county_code IS NOT NULL
  AND d.location IS NOT NULL
  AND d.category IN (
      'Activity', 'Wildlife', 'Hiking', 'Photography', 'Beach', 'Culture',
      'Food', 'Sightseeing', 'Shopping', 'Transport', 'Rest', 'Conservation'
  )
  AND cardinality(d.interest_tags) > 0
  AND d.short_description IS NOT NULL
  AND btrim(d.short_description) <> ''
  AND d.visit_duration_min_minutes IS NOT NULL
  AND d.visit_duration_max_minutes IS NOT NULL
  AND d.fee_status IS NOT NULL
  AND d.opening_hours_status IS NOT NULL
  AND d.hero_image_attribution IS NOT NULL
  AND btrim(d.hero_image_attribution) <> ''
  AND d.source IS NOT NULL
  AND btrim(d.source) <> ''

UNION ALL

SELECT
    NULL::UUID AS destination_id,
    'google_places'::TEXT AS provenance,
    c.place_id,
    c.name,
    NULL::TEXT AS slug,
    c.county_code,
    k.adm1_name AS county,
    c.locality,
    c.category,
    c.interest_tags,
    ARRAY[]::TEXT[] AS transport_modes,
    ARRAY[]::TEXT[] AS best_seasons,
    c.location,
    NULL::TEXT AS short_description,
    c.address,
    NULL::INTEGER AS duration_min_minutes,
    NULL::INTEGER AS duration_max_minutes,
    NULL::TEXT AS fee_status,
    NULL::TEXT AS fee_currency,
    NULL::INTEGER AS fee_amount,
    NULL::TEXT AS fee_basis,
    CASE WHEN c.opening_hours IS NOT NULL THEN 'unverified' ELSE NULL END AS opening_hours_status,
    c.opening_hours,
    c.hero_image_url,
    c.hero_image_attribution,
    NULL::TEXT AS hero_image_license,
    CASE WHEN c.hero_image_url IS NOT NULL THEN 'google_places' ELSE NULL END AS hero_image_source,
    c.rating,
    c.review_count,
    c.price_level,
    NULL::TIMESTAMP AS verified_at,
    c.source,
    'unverified_google_places'::TEXT AS eligibility
FROM composer_unverified_candidates c
JOIN kenya_counties k ON k.county_code = c.county_code
WHERE c.expires_at > now()
  AND c.category IN (
      'Activity', 'Wildlife', 'Hiking', 'Photography', 'Beach', 'Culture',
      'Food', 'Sightseeing', 'Shopping', 'Transport', 'Rest', 'Conservation'
  )
  AND cardinality(c.interest_tags) > 0;

CREATE OR REPLACE VIEW composer_county_coverage AS
SELECT
    k.county_code,
    k.adm1_name AS county,
    count(c.provenance) FILTER (WHERE c.provenance = 'curated') AS curated_count,
    count(c.provenance) FILTER (WHERE c.provenance = 'google_places') AS unverified_count,
    count(c.provenance) AS total_count,
    CASE
        WHEN count(c.provenance) = 0 THEN 'not_covered'
        WHEN count(c.provenance) FILTER (WHERE c.provenance = 'curated') < 3 THEN 'limited'
        ELSE 'covered'
    END AS coverage_status
FROM kenya_counties k
LEFT JOIN composer_catalog c ON c.county_code = k.county_code
GROUP BY k.county_code, k.adm1_name;
