ALTER TABLE user_profiles ADD COLUMN onboarding_completed BOOLEAN NOT NULL DEFAULT false;

UPDATE user_profiles SET onboarding_completed = true;
