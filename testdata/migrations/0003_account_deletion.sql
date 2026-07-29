ALTER TABLE stories ALTER COLUMN creator_id DROP NOT NULL;
ALTER TABLE itineraries ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE challenge_progress ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE course_enrollments ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE conservation_evidence ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE story_comments ALTER COLUMN author_id DROP NOT NULL;
ALTER TABLE report_jobs ALTER COLUMN requested_by DROP NOT NULL;
ALTER TABLE story_reports ALTER COLUMN reported_by DROP NOT NULL;
