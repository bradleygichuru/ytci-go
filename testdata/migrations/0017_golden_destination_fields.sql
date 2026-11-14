-- Structured curation fields for composer-ready destinations and the
-- county coverage report.
--
-- The golden fixtures (#216) use these fields. Unknown facts are recorded as
-- fee_status='not_verified' or opening_hours_status='unverified'; they are
-- never guessed.

ALTER TABLE destinations ADD COLUMN IF NOT EXISTS is_curated BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS visit_duration_min_minutes INTEGER;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS visit_duration_max_minutes INTEGER;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS fee_status TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS fee_currency TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS fee_amount INTEGER;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS fee_basis TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS opening_hours_status TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS opening_hours JSONB;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS hero_image_url TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS hero_image_attribution TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS hero_image_license TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS hero_image_source TEXT;
ALTER TABLE destinations ADD COLUMN IF NOT EXISTS hero_image_alt TEXT;

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_visit_duration_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_visit_duration_check
    CHECK (
        visit_duration_min_minutes IS NULL
        OR (
            visit_duration_min_minutes > 0
            AND visit_duration_max_minutes IS NOT NULL
            AND visit_duration_max_minutes >= visit_duration_min_minutes
        )
    );

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_fee_status_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_fee_status_check
    CHECK (fee_status IS NULL OR fee_status IN ('verified', 'reported', 'not_verified', 'free'));

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_fee_basis_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_fee_basis_check
    CHECK (
        fee_status IS NULL
        OR fee_status = 'not_verified'
        OR (fee_basis IS NOT NULL AND btrim(fee_basis) <> '')
    );

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_opening_hours_status_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_opening_hours_status_check
    CHECK (opening_hours_status IS NULL OR opening_hours_status IN ('verified', 'unverified'));

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_opening_hours_verified_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_opening_hours_verified_check
    CHECK (opening_hours_status <> 'verified' OR opening_hours IS NOT NULL);

ALTER TABLE destinations DROP CONSTRAINT IF EXISTS destinations_hero_attribution_check;
ALTER TABLE destinations
    ADD CONSTRAINT destinations_hero_attribution_check
    CHECK (hero_image_url IS NULL OR (hero_image_attribution IS NOT NULL AND btrim(hero_image_attribution) <> ''));

CREATE INDEX IF NOT EXISTS destinations_curated_idx
    ON destinations (is_curated, status, county_code)
    WHERE is_curated;

-- Monthly county coverage: curated and published destinations per county.
CREATE OR REPLACE VIEW catalog_county_coverage AS
SELECT
    k.county_code,
    k.adm1_name AS county,
    count(d.id) FILTER (WHERE d.is_curated AND d.status = 'published') AS curated_count,
    count(d.id) FILTER (WHERE d.status = 'published') AS published_count
FROM kenya_counties k
LEFT JOIN destinations d ON d.county_code = k.county_code
GROUP BY k.county_code, k.adm1_name;
