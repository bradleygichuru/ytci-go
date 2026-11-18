-- Verification for the four-site Then & Now seed.
-- Run after `go run ./cmd/seed-then-now`:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f cmd/seed-then-now/verify.sql
--
-- The SELECT mirrors the public `publishedSiteSelect`: a site is visible only
-- when the site, its experience, and the live revision are all published.

SELECT s.slug,
       s.name,
       s.access_mode,
       s.sensitivity,
       e.title,
       r.revision_number AS live_revision,
       r.status AS live_revision_status,
       (r.snapshot->>'duration')::int AS duration,
       jsonb_array_length(r.snapshot->'textCards') AS text_cards,
       r.snapshot->'renditionKeys'->>'1080' AS rendition_1080,
       s.public_rights_line
FROM heritage_sites s
JOIN then_now_experiences e ON e.site_id = s.id
JOIN then_now_revisions r
  ON r.experience_id = e.id AND r.revision_number = e.live_revision_number
WHERE s.status = 'published'
  AND e.status = 'published'
  AND r.status = 'published'
ORDER BY s.name;

DO $$
DECLARE
  published_count int;
  matched_count int;
BEGIN
  SELECT count(*) INTO published_count
  FROM heritage_sites s
  WHERE s.status = 'published'
    AND EXISTS (
      SELECT 1 FROM then_now_experiences e
      JOIN then_now_revisions r
        ON r.experience_id = e.id AND r.revision_number = e.live_revision_number
      WHERE e.site_id = s.id AND e.status = 'published' AND r.status = 'published'
    );

  IF published_count <> 4 THEN
    RAISE EXCEPTION 'expected 4 published Then & Now sites, found %', published_count;
  END IF;

  SELECT count(*) INTO matched_count
  FROM (VALUES
    ('nairobi-railway-museum', 'camera_video', 'standard'),
    ('nairobi-war-cemetery', 'video_only', 'sensitive'),
    ('african-heritage-house', 'not_available', 'standard'),
    ('nabongo-cultural-centre', 'not_available', 'standard')
  ) AS want(slug, access_mode, sensitivity)
  JOIN heritage_sites s ON s.slug = want.slug
  WHERE s.status = 'published'
    AND s.access_mode = want.access_mode
    AND s.sensitivity = want.sensitivity
    AND EXISTS (
      SELECT 1 FROM then_now_experiences e
      JOIN then_now_revisions r
        ON r.experience_id = e.id AND r.revision_number = e.live_revision_number
      WHERE e.site_id = s.id AND e.status = 'published' AND r.status = 'published'
    );

  IF matched_count <> 4 THEN
    RAISE EXCEPTION 'published site access/sensitivity modes do not match the four-site seed (matched %)', matched_count;
  END IF;
END $$;
