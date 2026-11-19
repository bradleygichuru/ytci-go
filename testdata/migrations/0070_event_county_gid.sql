-- Mirrors TanStack 0034: the MICE events API reads county_gid, author_ref and
-- editorial_theme, but no earlier migration created them.
ALTER TABLE events ADD COLUMN IF NOT EXISTS county_gid integer REFERENCES kenya_counties(gid);
ALTER TABLE events ADD COLUMN IF NOT EXISTS author_ref text;
ALTER TABLE events ADD COLUMN IF NOT EXISTS editorial_theme text;
CREATE INDEX IF NOT EXISTS events_county_gid_idx ON events (county_gid);
