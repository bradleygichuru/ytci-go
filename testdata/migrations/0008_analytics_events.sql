CREATE TABLE analytics_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    event TEXT NOT NULL,
    properties JSONB DEFAULT '{}'::jsonb,
    platform TEXT,
    app_version TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);
