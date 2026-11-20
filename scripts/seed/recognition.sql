BEGIN;

INSERT INTO recognition.badge_definitions (badge_key, name_en, name_sw, family, criteria, privacy_class, hidden, public_eligible, status)
VALUES
 ('event_attended','Event attendee','Mhudhuria hafla','events','{"label":"Verified check-in at a listed event"}'::jsonb,'public',false,true,'published'),
 ('trail_keeper','Trail keeper','Mlinzi wa njia','heritage','{"label":"Complete three mapped heritage trails"}'::jsonb,'public',false,true,'published'),
 ('county_explorer','County explorer','Mvumbuzi wa kaunti','heritage','{"label":"Visit ten counties"}'::jsonb,'public',false,true,'published'),
 ('species_scholar','Species scholar','Mwanasayansi wa spishi','heritage','{"label":"Finish a species course"}'::jsonb,'public',false,true,'published'),
 ('season_finisher','Season finisher','Mkamilishaji wa msimu','season','{"label":"Complete all missions in a season"}'::jsonb,'public',false,true,'published'),
 ('city_scout','City scout','Mpelelezi wa mji','events','{"label":"Attend an event in Nairobi"}'::jsonb,'public',false,true,'published')
ON CONFLICT (badge_key) DO UPDATE SET status='published', name_sw=EXCLUDED.name_sw, criteria=EXCLUDED.criteria;

INSERT INTO recognition.rules (rule_key, version, source_type, status, effective_from, criteria, xp_amount, badge_key)
VALUES
 ('event.attendance', 3, 'event_check_in', 'published', now() - interval '30 days', '{"event_class":"any"}'::jsonb, 120, 'event_attended'),
 ('heritage.visit', 1, 'heritage_visit', 'published', now() - interval '30 days', '{"sites":3}'::jsonb, 180, 'trail_keeper'),
 ('species.course', 1, 'species_course', 'published', now() - interval '30 days', '{"completion":true}'::jsonb, 200, 'species_scholar')
ON CONFLICT (rule_key, version) DO UPDATE SET status='published', xp_amount=EXCLUDED.xp_amount;

INSERT INTO recognition.seasons (season_key, name_en, name_sw, starts_at, ends_at, status, xp_award)
VALUES ('2026-q4','Season of the Rift','Msimu wa Rift', date_trunc('quarter', now()), date_trunc('quarter', now()) + interval '3 months', 'active', 500)
ON CONFLICT (season_key) DO UPDATE SET status='active', ends_at=EXCLUDED.ends_at;

COMMIT;
