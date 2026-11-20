-- Composer engine persistence: suggestion cache, daily usage, routing cache.

CREATE TABLE IF NOT EXISTS composer_suggestion_cache (
    input_hash TEXT PRIMARY KEY,
    catalog_version TEXT NOT NULL,
    engine_version TEXT NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS composer_suggestion_cache_expiry_idx
    ON composer_suggestion_cache (expires_at);

CREATE TABLE IF NOT EXISTS composer_suggestion_usage (
    user_id TEXT NOT NULL,
    usage_date DATE NOT NULL,
    count INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
    PRIMARY KEY (user_id, usage_date)
);

CREATE TABLE IF NOT EXISTS routing_cache (
    cache_key TEXT PRIMARY KEY,
    mode TEXT NOT NULL,
    response JSONB NOT NULL,
    provider TEXT NOT NULL,
    estimated BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS routing_cache_expiry_idx
    ON routing_cache (expires_at);

CREATE TABLE IF NOT EXISTS routing_usage (
    usage_date DATE PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0)
);
