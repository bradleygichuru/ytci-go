-- Daily discovery runs for county/route-biased Google Places candidates.
-- A successful run is reused for the UTC date bucket; candidates themselves
-- expire after 30 days in composer_unverified_candidates.

CREATE TABLE IF NOT EXISTS composer_discovery_runs (
    bucket_date DATE NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('county', 'route')),
    scope_key TEXT NOT NULL,
    county_code TEXT
        REFERENCES kenya_counties(county_code) ON UPDATE CASCADE,
    request_count INTEGER NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    candidate_count INTEGER NOT NULL DEFAULT 0 CHECK (candidate_count >= 0),
    candidate_place_ids TEXT[] NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    error TEXT,
    started_at TIMESTAMP NOT NULL DEFAULT now(),
    finished_at TIMESTAMP,
    PRIMARY KEY (bucket_date, scope_kind, scope_key)
);

CREATE INDEX IF NOT EXISTS composer_discovery_runs_bucket_idx
    ON composer_discovery_runs (bucket_date, status);
CREATE INDEX IF NOT EXISTS composer_discovery_runs_county_idx
    ON composer_discovery_runs (county_code);
