-- Audio Stories media foundation: allow the audio_story owner type and
-- unlinked uploads until a media asset is attached to a story/revision.
ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_entity_type_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_entity_type_check
  CHECK (entity_type IN ('destination', 'story', 'event', 'course', 'conservation_activity', 'user', 'audio_story', 'unlinked'));
ALTER TABLE media_assets ALTER COLUMN entity_id DROP NOT NULL;
