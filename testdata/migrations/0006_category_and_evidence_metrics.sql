-- Add category column to courses (matching tanstack migration 0008)
ALTER TABLE courses ADD COLUMN IF NOT EXISTS category TEXT;

-- Add trees_planted, hours_spent, lat, lng to conservation_evidence (matching tanstack migration 0009)
ALTER TABLE conservation_evidence ADD COLUMN IF NOT EXISTS trees_planted INTEGER;
ALTER TABLE conservation_evidence ADD COLUMN IF NOT EXISTS hours_spent DOUBLE PRECISION;
ALTER TABLE conservation_evidence ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION;
ALTER TABLE conservation_evidence ADD COLUMN IF NOT EXISTS lng DOUBLE PRECISION;
