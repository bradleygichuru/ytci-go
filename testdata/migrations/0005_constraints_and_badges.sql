-- Unified badges table across challenges, courses, and conservation
CREATE TABLE badges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    badge_name TEXT NOT NULL,
    badge_icon_url TEXT,
    source_type TEXT NOT NULL CHECK (source_type IN ('challenge', 'course', 'conservation')),
    source_id UUID NOT NULL,
    source_title TEXT,
    awarded_at TIMESTAMP NOT NULL DEFAULT now()
);
CREATE INDEX idx_badges_user_id ON badges(user_id);

-- Conservation participant records (one per user per activity)
CREATE TABLE conservation_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    activity_id UUID NOT NULL REFERENCES conservation_activities(id) ON DELETE CASCADE,
    joined_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE(user_id, activity_id)
);

-- Unique constraints for idempotent joins and enrollments
CREATE UNIQUE INDEX IF NOT EXISTS idx_course_enrollments_user_course ON course_enrollments(user_id, course_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenge_progress_user_challenge ON challenge_progress(user_id, challenge_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conservation_evidence_user_activity ON conservation_evidence(user_id, activity_id);

-- One quiz per course
CREATE UNIQUE INDEX IF NOT EXISTS idx_quizzes_course_id ON quizzes(course_id);

-- Add badge metadata to conservation_activities (parallel to challenges)
ALTER TABLE conservation_activities ADD COLUMN IF NOT EXISTS badge_name TEXT;
ALTER TABLE conservation_activities ADD COLUMN IF NOT EXISTS badge_icon_url TEXT;

-- Add badge metadata to courses (parallel to challenges)
ALTER TABLE courses ADD COLUMN IF NOT EXISTS badge_name TEXT;
ALTER TABLE courses ADD COLUMN IF NOT EXISTS badge_icon_url TEXT;

-- Add updated_at to conservation_evidence (needed for evidence resubmission)
ALTER TABLE conservation_evidence ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP NOT NULL DEFAULT now();

-- Expand conservation_evidence status to allow in_progress (for reject → resubmit flow)
ALTER TABLE conservation_evidence DROP CONSTRAINT IF EXISTS conservation_evidence_status_check;
ALTER TABLE conservation_evidence ADD CONSTRAINT conservation_evidence_status_check
    CHECK (status IN ('pending', 'in_progress', 'approved', 'rejected'));
