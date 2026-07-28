CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    county TEXT NOT NULL,
    locality TEXT,
    category TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    location geometry(Point, 4326),
    map_label TEXT,
    access_route TEXT,
    distance_reference TEXT,
    short_description TEXT,
    full_description TEXT,
    significance TEXT,
    history TEXT,
    things_to_do TEXT,
    suitable_audiences TEXT,
    duration TEXT,
    difficulty TEXT,
    seasonality TEXT,
    indicative_fees TEXT,
    opening_info TEXT,
    transport_notes TEXT,
    accessibility TEXT,
    facilities TEXT,
    safety_notes TEXT,
    source TEXT,
    content_owner TEXT,
    verification_status TEXT,
    last_updated TIMESTAMP,
    review_date TIMESTAMP,
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    organizer TEXT NOT NULL,
    county TEXT NOT NULL,
    venue TEXT,
    event_date DATE NOT NULL,
    end_date DATE,
    type TEXT NOT NULL CHECK (type IN ('cultural', 'sports', 'conservation', 'tourism')),
    status TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'postponed', 'cancelled')),
    description TEXT,
    contact_email TEXT,
    contact_phone TEXT,
    image_url TEXT,
    reminder_enabled BOOLEAN DEFAULT false,
    reminder_minutes INTEGER,
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE stories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL,
    destination_id UUID,
    caption TEXT,
    journal TEXT,
    tags TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending', 'approved', 'rejected')),
    moderated_by UUID,
    moderation_note TEXT,
    moderated_at TIMESTAMP,
    like_count INTEGER DEFAULT 0,
    save_count INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE media_assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('destination', 'story', 'event', 'course', 'conservation_activity', 'user')),
    entity_id UUID NOT NULL,
    object_key TEXT NOT NULL,
    thumbnail_key TEXT,
    type TEXT NOT NULL CHECK (type IN ('image', 'video', 'audio', 'pdf', '360')),
    alt_text TEXT,
    caption TEXT,
    credit TEXT,
    rights_status TEXT,
    file_size_bytes INTEGER,
    duration INTEGER,
    original_name TEXT,
    display_order INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'processing', 'ready', 'failed', 'pending_review', 'removed')),
    moderated_by UUID,
    moderation_note TEXT,
    moderated_at TIMESTAMP,
    uploaded_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE courses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    description TEXT,
    difficulty TEXT NOT NULL CHECK (difficulty IN ('beginner', 'intermediate', 'advanced')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    image_url TEXT,
    pass_threshold INTEGER DEFAULT 70,
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE lessons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT,
    content_type TEXT NOT NULL CHECK (content_type IN ('video', 'pdf', 'text')),
    content_url TEXT,
    duration INTEGER,
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE quizzes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    lesson_id UUID REFERENCES lessons(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    questions JSONB NOT NULL,
    pass_threshold INTEGER DEFAULT 70,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE course_enrollments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    completed_lesson_ids JSONB DEFAULT '[]',
    quiz_attempts JSONB DEFAULT '{}',
    certificate_url TEXT,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    description TEXT,
    rules TEXT,
    badge_name TEXT,
    badge_icon_url TEXT,
    eligibility JSONB,
    start_date DATE,
    end_date DATE,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'ended')),
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE challenge_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'joined' CHECK (status IN ('joined', 'in_progress', 'submitted', 'approved', 'rejected')),
    progress JSONB DEFAULT '{}',
    evidence JSONB,
    moderated_by UUID,
    moderation_note TEXT,
    badge_awarded_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE conservation_activities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    organizer TEXT NOT NULL,
    description TEXT,
    location geometry(Point, 4326),
    location_label TEXT,
    privacy_level TEXT NOT NULL DEFAULT 'public' CHECK (privacy_level IN ('public', 'approximate', 'hidden')),
    event_date DATE,
    impact_metric TEXT,
    impact_target INTEGER,
    participant_limit INTEGER,
    current_participants INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'full', 'completed', 'cancelled')),
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE conservation_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    activity_id UUID NOT NULL REFERENCES conservation_activities(id) ON DELETE CASCADE,
    description TEXT,
    media_ids TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    moderated_by UUID,
    moderation_note TEXT,
    moderated_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    banner_url TEXT,
    type TEXT NOT NULL CHECK (type IN ('home_banner', 'featured_destination', 'push_notification', 'seasonal')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'paused', 'ended')),
    start_date DATE NOT NULL,
    end_date DATE,
    target_url TEXT,
    destination_id UUID,
    audience TEXT,
    created_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE push_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    token TEXT NOT NULL UNIQUE,
    platform TEXT CHECK (platform IN ('ios', 'android')),
    device_info TEXT,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION notify_push_scheduled()
RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('push_scheduled', NEW.id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE push_notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID REFERENCES campaigns(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    image_url TEXT,
    data TEXT,
    target_audience TEXT,
    scheduled_at TIMESTAMP,
    sent_at TIMESTAMP,
    recipient_count INTEGER,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'failed')),
    sent_by UUID,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE itineraries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    title TEXT NOT NULL,
    inputs JSONB NOT NULL,
    total_budget TEXT,
    disclaimer TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'saved', 'exported')),
    version INTEGER DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE itinerary_stops (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    destination_id UUID REFERENCES destinations(id) ON DELETE SET NULL,
    day INTEGER NOT NULL,
    display_order INTEGER NOT NULL,
    title TEXT,
    description TEXT,
    estimated_duration TEXT,
    estimated_cost TEXT,
    travel_from TEXT,
    notes TEXT
);

CREATE TABLE bucket_list_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    destination_id UUID NOT NULL REFERENCES destinations(id) ON DELETE CASCADE,
    visited BOOLEAN DEFAULT false,
    visited_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE app_opens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    platform TEXT CHECK (platform IN ('ios', 'android')),
    app_version TEXT,
    opened_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE report_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requested_by UUID NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('csv', 'pdf')),
    date_from DATE NOT NULL,
    date_to DATE NOT NULL,
    sections TEXT,
    status TEXT NOT NULL DEFAULT 'generating' CHECK (status IN ('generating', 'ready', 'failed')),
    file_key TEXT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    completed_at TIMESTAMP
);

CREATE TABLE user_profiles (
    user_id TEXT PRIMARY KEY,
    age_range TEXT,
    county TEXT,
    languages TEXT,
    preferences TEXT,
    consent_granted_at TIMESTAMP,
    created_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE kenya_counties (
    gid INTEGER PRIMARY KEY,
    adm1_name TEXT NOT NULL,
    adm1_pcode TEXT,
    area_sqkm DOUBLE PRECISION,
    geom geometry(MultiPolygon, 4326)
);
CREATE INDEX kenya_counties_geom_idx ON kenya_counties USING GIST (geom);

CREATE TABLE story_interactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    interaction_type TEXT NOT NULL CHECK (interaction_type IN ('like', 'save')),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE (user_id, story_id, interaction_type)
);
CREATE INDEX story_interactions_story_idx ON story_interactions (story_id);

-- GIST indexes for spatial queries
CREATE INDEX IF NOT EXISTS destinations_location_idx ON destinations USING GIST (location);
CREATE INDEX IF NOT EXISTS conservation_activities_location_idx ON conservation_activities USING GIST (location);

-- B-tree indexes for common filters
CREATE INDEX IF NOT EXISTS destinations_county_category_status_idx ON destinations (county, category, status);
CREATE INDEX IF NOT EXISTS stories_creator_idx ON stories (creator_id);
CREATE INDEX IF NOT EXISTS stories_status_idx ON stories (status);
CREATE INDEX IF NOT EXISTS stories_created_idx ON stories (created_at);
CREATE INDEX IF NOT EXISTS events_county_type_status_date_idx ON events (county, type, status, event_date);
CREATE INDEX IF NOT EXISTS media_entity_type_id_idx ON media_assets (entity_type, entity_id);
CREATE TABLE event_saves (
    user_id UUID NOT NULL,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, event_id)
);

CREATE INDEX IF NOT EXISTS push_tokens_user_active_idx ON push_tokens (user_id, is_active);
CREATE INDEX IF NOT EXISTS bucket_list_user_idx ON bucket_list_items (user_id);

CREATE TABLE story_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    reported_by UUID NOT NULL,
    reason TEXT NOT NULL,
    details TEXT,
    reviewed BOOLEAN DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TRIGGER push_notify_trigger AFTER INSERT ON push_notifications
FOR EACH ROW EXECUTE FUNCTION notify_push_scheduled();

CREATE TABLE pending_media_uploads (
    object_key TEXT PRIMARY KEY,
    user_id UUID NOT NULL,
    content_type TEXT NOT NULL,
    file_size BIGINT NOT NULL,
    expires_at TIMESTAMP NOT NULL DEFAULT (now() + interval '5 minutes'),
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

ALTER TABLE user_profiles ADD COLUMN display_name TEXT;

CREATE TABLE story_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL,
    parent_id UUID REFERENCES story_comments(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'deleted')),
    like_count INTEGER DEFAULT 0,
    moderation_note TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);
CREATE INDEX story_comments_story_idx ON story_comments (story_id);
CREATE INDEX story_comments_parent_idx ON story_comments (parent_id);

CREATE TABLE comment_interactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    comment_id UUID NOT NULL REFERENCES story_comments(id) ON DELETE CASCADE,
    interaction_type TEXT NOT NULL CHECK (interaction_type = 'like'),
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE (user_id, comment_id, interaction_type)
);
CREATE INDEX comment_interactions_comment_idx ON comment_interactions (comment_id);
