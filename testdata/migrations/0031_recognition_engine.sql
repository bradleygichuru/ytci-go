-- Recognition engine support (ticket #365, migration range 0030-0042).
-- Owner: stream:recognition.
--
-- Additive only. 0030 remains the contract slice; this migration adds the
-- columns and seed rows the engine needs to run. Nothing here drops or
-- narrows a contract column.

-- ---------------------------------------------------------------------------
-- Engine columns
-- ---------------------------------------------------------------------------

-- The ledger records when the recognised action happened, not just when the
-- row was written. Weekly caps (Audio 5/week, community stories 3/week) and
-- backfill windows read this column.
ALTER TABLE recognition.xp_entries
    ADD COLUMN IF NOT EXISTS occurred_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS recognition_xp_entries_cap_idx
    ON recognition.xp_entries (subject_id, source_type, occurred_at DESC)
    WHERE reason = 'award';

-- Internal rule hint. One event type can have several published rules
-- (challenge difficulty, optional heritage rewards); the hint records which
-- immutable rule the source adapter selected. It is not a payload field.
ALTER TABLE recognition.events
    ADD COLUMN IF NOT EXISTS rule_key_hint text;

-- Publish requires a simulation, and reviewer != author. The actor ids make
-- those checks possible without a second table.
ALTER TABLE recognition.rules
    ADD COLUMN IF NOT EXISTS simulated_at timestamptz,
    ADD COLUMN IF NOT EXISTS simulated_by text REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS published_at timestamptz;

-- A season carries its mission families and its publish/close lifecycle.
ALTER TABLE recognition.seasons
    ADD COLUMN IF NOT EXISTS mission_families text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS published_at timestamptz,
    ADD COLUMN IF NOT EXISTS frozen_at timestamptz,
    ADD COLUMN IF NOT EXISTS closed_at timestamptz,
    ADD COLUMN IF NOT EXISTS snapshot jsonb;

-- Tie-break for the final ranking step (earliest completion wins).
ALTER TABLE recognition.leaderboard_entries
    ADD COLUMN IF NOT EXISTS last_completion_at timestamptz;

ALTER TABLE recognition.moderation_cases
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

-- A mission is a curated target, not a daily quest. The target is immutable
-- once published; a change creates a new track key.
ALTER TABLE recognition.progress_tracks
    ADD COLUMN IF NOT EXISTS target_value integer NOT NULL DEFAULT 1
        CHECK (target_value > 0);

-- Occurrence identity is durable: one subject can experience an occurrence
-- once. Re-emitting the same envelope is a no-op, not a second ledger row.
CREATE UNIQUE INDEX IF NOT EXISTS recognition_events_occurrence_unique
    ON recognition.events (subject, occurrence_key);

CREATE INDEX IF NOT EXISTS recognition_events_subject_type_idx
    ON recognition.events (subject, type, time DESC);

-- ---------------------------------------------------------------------------
-- Badge catalogue seed (names in EN and SW; publishing requires both).
-- ---------------------------------------------------------------------------

INSERT INTO recognition.badge_definitions
    (badge_key, name_en, name_sw, family, criteria, icon_key, colour_token,
     privacy_class, hidden, public_eligible, status, version)
VALUES
    ('heritage_site_visit', 'Heritage site visited', 'Tovuti ya urithi imetembelewa',
     'heritage', '{"verifiedSites": 1}', 'site', 'heritage',
     'private', false, true, 'published', 1),
    ('heritage_trail_complete', 'Trail completed', 'Njia imekamilika',
     'heritage', '{"trails": 1}', 'trail', 'heritage',
     'private', false, true, 'published', 1),
    ('county_collected', 'County collected', 'Kaunti imekusanywa',
     'heritage', '{"counties": 1}', 'county', 'heritage',
     'private', false, true, 'published', 1),
    ('conservation_participant', 'Conservation participant', 'Mshiriki wa uhifadhi',
     'conservation', '{"verifiedParticipations": 1}', 'leaf', 'conservation',
     'private', false, true, 'published', 1),
    ('challenge_complete', 'Challenge complete', 'Changamoto imekamilika',
     'challenges', '{"moderatedCompletions": 1}', 'flag', 'challenge',
     'private', false, true, 'published', 1),
    ('course_complete', 'Course complete', 'Kozi imekamilika',
     'learning', '{"passedQuizzes": 1}', 'book', 'learning',
     'private', false, true, 'published', 1),
    ('event_attended', 'Event attendee', 'Mhudhuriaji wa tukio',
     'events', '{"verifiedCheckIns": 1}', 'ticket', 'events',
     'private', false, true, 'published', 1),
    ('audio_story_complete', 'Story listened', 'Hadithi imesikilizwa',
     'audio', '{"completedStories": 1}', 'headphones', 'audio',
     'private', false, false, 'published', 1),
    ('community_contributor', 'Community contributor', 'Mchangiaji wa jamii',
     'community', '{"approvedStories": 1}', 'pen', 'community',
     'private', false, false, 'published', 1),
    ('season_complete', 'Season complete', 'Msimu umekamilika',
     'seasons', '{"seasonCompleted": true}', 'calendar', 'season',
     'private', false, false, 'published', 1)
ON CONFLICT (badge_key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Published rules. Rules are immutable; a change inserts a new version.
-- `criteria.variant` distinguishes optional heritage rewards that share an
-- event type. Emitters that know the variant pass a rule-key hint.
-- ---------------------------------------------------------------------------

INSERT INTO recognition.rules
    (rule_key, version, source_type, status, effective_from, criteria, xp_amount, badge_key)
VALUES
    ('heritage.site.visit', 1, 'heritage_site', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.heritage.visit.verified.v1","variant":"default"}', 100, 'heritage_site_visit'),
    ('heritage.trail.complete', 1, 'heritage_trail', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.heritage.trail.completed.v1","variant":"default"}', 250, 'heritage_trail_complete'),
    ('heritage.county.collected', 1, 'county', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.heritage.county.collected.v1","variant":"default"}', 150, 'county_collected'),
    ('conservation.participation', 1, 'conservation_activity', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.conservation.participation.verified.v1","variant":"default"}', 150,
     'conservation_participant'),
    ('challenge.standard', 1, 'challenge', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.challenge.completed.v1","variant":"default","difficulty":"standard"}', 50,
     'challenge_complete'),
    ('challenge.advanced', 1, 'challenge', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.challenge.completed.v1","variant":"advanced","difficulty":"advanced"}', 100,
     'challenge_complete'),
    ('challenge.flagship', 1, 'challenge', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.challenge.completed.v1","variant":"flagship","difficulty":"flagship"}', 150,
     'challenge_complete'),
    ('course.completion', 1, 'course', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.course.completed.v1","variant":"default"}', 150, 'course_complete'),
    ('event.attendance', 1, 'event', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.event.attended.v1","variant":"default"}', 50, 'event_attended'),
    ('audio.story.completed', 1, 'audio_story', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.audio.story.completed.v1","variant":"default","weekly_cap":5}', 25,
     'audio_story_complete'),
    ('community.story.approved', 1, 'community_story', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.community.story.approved.v1","variant":"default","weekly_cap":3}', 50,
     'community_contributor'),
    ('explore.destination.progress', 1, 'destination', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.explore.destination.progressed.v1","variant":"default"}', 0, NULL),
    ('season.completion', 1, 'season', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.season.completed.v1","variant":"default"}', 500, 'season_complete'),
    ('heritage.trail_stop', 1, 'heritage_site', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.heritage.visit.verified.v1","variant":"trail_stop"}', 25, NULL),
    ('heritage.story_cut', 1, 'heritage_site', 'published', '2026-01-01T00:00:00Z',
     '{"event_type":"ke.heritage.visit.verified.v1","variant":"story_cut"}', 20, NULL)
ON CONFLICT (rule_key, version) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Deterministic missions (progress tracks). Five families, no daily reset.
-- ---------------------------------------------------------------------------

INSERT INTO recognition.progress_tracks
    (track_key, family, name_en, name_sw, criteria, xp_reward, badge_key, status, target_value)
VALUES
    ('county_explorer', 'county_collection', 'County Explorer', 'Mvumbuzi wa Kaunti',
     '{"source_type":"county","count":"distinct"}', 250, 'county_collected', 'published', 47),
    ('mice_explorer', 'mission', 'MICE Explorer', 'Mvumbuzi wa Matukio',
     '{"source_type":"event","count":"distinct"}', 100, 'event_attended', 'published', 5),
    ('conservation_sprint', 'mission', 'Conservation Sprint', 'Mbio za Uhifadhi',
     '{"source_type":"conservation_activity","count":"distinct"}', 150,
     'conservation_participant', 'published', 3),
    ('learning_path', 'course_path', 'Learning Path', 'Njia ya Kujifunza',
     '{"source_type":"course","count":"distinct"}', 150, 'course_complete', 'published', 3),
    ('listener', 'mission', 'Listener', 'Msikilizaji',
     '{"source_type":"audio_story","count":"distinct"}', 100,
     'audio_story_complete', 'published', 10)
ON CONFLICT (track_key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Pilot seasons. A season resets the board, never XP, levels or badges.
-- ---------------------------------------------------------------------------

INSERT INTO recognition.seasons
    (season_key, name_en, name_sw, starts_at, ends_at, status, badge_key, xp_award,
     mission_families)
VALUES
    ('2026-q4', 'Season of the Rift', 'Msimu wa Bonde',
     '2026-10-01T00:00:00Z', '2026-12-31T23:59:59Z', 'active', 'season_complete', 500,
     ARRAY['county_collection','mission','course_path']),
    ('2027-q1', 'Season of the Coast', 'Msimu wa Pwani',
     '2027-01-01T00:00:00Z', '2027-03-31T23:59:59Z', 'upcoming', 'season_complete', 500,
     ARRAY['county_collection','mission','course_path'])
ON CONFLICT (season_key) DO NOTHING;
