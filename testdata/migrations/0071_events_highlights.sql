-- Mirrors TanStack 0039: the MICE create path writes events.highlights.
ALTER TABLE events ADD COLUMN IF NOT EXISTS highlights jsonb NOT NULL DEFAULT '[]'::jsonb;
