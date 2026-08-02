-- Backfill display_name for existing users who never set it.
-- user_profiles.display_name is nullable; without this, the COALESCE
-- fallback in leaderboard and profile queries always returns 'Anonymous'.
-- The users table (better-auth) is in the same database and holds the
-- sign-up name that should be the canonical display name.
UPDATE user_profiles
SET display_name = u.name,
    updated_at   = now()
FROM users u
WHERE user_profiles.user_id = u.id
  AND user_profiles.display_name IS NULL;
