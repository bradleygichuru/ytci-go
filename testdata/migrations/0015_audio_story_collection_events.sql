-- Collection lifecycle transitions are recorded in audio_story_events, which
-- is keyed by story for revision-scoped events. Collection-scoped events have
-- no story, so allow story_id to be NULL and keep collection_id as the scope.
ALTER TABLE "audio_story_events" ALTER COLUMN "story_id" DROP NOT NULL;
