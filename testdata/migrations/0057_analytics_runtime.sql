-- Analytics runtime support (ticket #373, migration range 0056-0068).
-- Owner: stream:analytics.
--
-- 0056 froze the tables. This migration adds the runtime pieces the handlers
-- need: the guest install -> analytics_id binding, action property schemas and
-- the erasure tombstone used by both admin erasure and account deletion.

-- ---------------------------------------------------------------------------
-- Guest installs resolve to a stable analytics_id
-- ---------------------------------------------------------------------------

ALTER TABLE analytics_installs ADD COLUMN IF NOT EXISTS analytics_id uuid;

CREATE INDEX IF NOT EXISTS analytics_installs_analytics_idx
    ON analytics_installs (analytics_id);

CREATE INDEX IF NOT EXISTS analytics_events_analytics_received_idx
    ON analytics_events (analytics_id, received_at DESC);

CREATE INDEX IF NOT EXISTS analytics_ingest_batches_analytics_idx
    ON analytics_ingest_batches (analytics_id, received_at DESC);

-- ---------------------------------------------------------------------------
-- Action property allow-lists
-- ---------------------------------------------------------------------------

INSERT INTO analytics_event_schemas (event_name, event_family, property_schema)
VALUES
    ('search_performed', 'action',
     '{"type":"object","properties":{"surface":{"type":"string"},"query_length_bucket":{"type":"string"},"result_count":{"type":"integer"},"selected_rank":{"type":"integer"},"external_selection":{"type":"boolean"},"query_hash":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('destination_viewed', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"surface":{"type":"string"},"source":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('destination_saved', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"surface":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('destination_unsaved', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"surface":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('bucket_item_added', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"source":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('bucket_item_visited', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"verified":{"type":"boolean"}},"additionalProperties":false}'::jsonb),
    ('itinerary_created', 'action',
     '{"type":"object","properties":{"stop_count":{"type":"integer"},"day_count":{"type":"integer"},"source":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('itinerary_updated', 'action',
     '{"type":"object","properties":{"stop_count":{"type":"integer"},"day_count":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('itinerary_stop_added', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"day_index":{"type":"integer"},"position":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('event_viewed', 'action',
     '{"type":"object","properties":{"event_id":{"type":"string"},"event_class":{"type":"string"},"surface":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('event_saved', 'action',
     '{"type":"object","properties":{"event_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('event_registration_started', 'action',
     '{"type":"object","properties":{"event_id":{"type":"string"},"ticket_type":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('event_registration_completed', 'action',
     '{"type":"object","properties":{"event_id":{"type":"string"},"registration_id":{"type":"string"},"ticket_count":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('event_checkin_scanned', 'action',
     '{"type":"object","properties":{"event_id":{"type":"string"},"result":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('challenge_joined', 'action',
     '{"type":"object","properties":{"challenge_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('challenge_evidence_submitted', 'action',
     '{"type":"object","properties":{"challenge_id":{"type":"string"},"evidence_type":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('conservation_joined', 'action',
     '{"type":"object","properties":{"activity_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('conservation_evidence_submitted', 'action',
     '{"type":"object","properties":{"activity_id":{"type":"string"},"evidence_type":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('course_enrolled', 'action',
     '{"type":"object","properties":{"course_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('lesson_completed', 'action',
     '{"type":"object","properties":{"course_id":{"type":"string"},"lesson_id":{"type":"string"},"position":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('quiz_submitted', 'action',
     '{"type":"object","properties":{"course_id":{"type":"string"},"score_bucket":{"type":"string"},"passed":{"type":"boolean"}},"additionalProperties":false}'::jsonb),
    ('course_completed', 'action',
     '{"type":"object","properties":{"course_id":{"type":"string"},"lesson_count":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('story_created', 'action',
     '{"type":"object","properties":{"destination_id":{"type":"string"},"media_count":{"type":"integer"}},"additionalProperties":false}'::jsonb),
    ('story_liked', 'action',
     '{"type":"object","properties":{"story_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('story_saved', 'action',
     '{"type":"object","properties":{"story_id":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('heritage_portal_opened', 'action',
     '{"type":"object","properties":{"site_slug":{"type":"string"},"surface":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('heritage_audio_played', 'action',
     '{"type":"object","properties":{"site_slug":{"type":"string"},"duration_bucket":{"type":"string"},"language":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('heritage_visit_marked', 'action',
     '{"type":"object","properties":{"site_slug":{"type":"string"},"verified":{"type":"boolean"}},"additionalProperties":false}'::jsonb),
    ('marketplace_handoff', 'action',
     '{"type":"object","properties":{"business_id":{"type":"string"},"surface":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('alert_opened', 'action',
     '{"type":"object","properties":{"alert_id":{"type":"string"},"category":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('notification_opened', 'action',
     '{"type":"object","properties":{"surface":{"type":"string"},"category":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('app_open', 'action',
     '{"type":"object","properties":{"cold_start":{"type":"boolean"},"source":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('consent_changed', 'action',
     '{"type":"object","properties":{"scope":{"type":"string"},"state":{"type":"string"},"version":{"type":"string"}},"additionalProperties":false}'::jsonb),
    ('recognition_optin', 'action',
     '{"type":"object","properties":{"surface":{"type":"string"},"state":{"type":"string"}},"additionalProperties":false}'::jsonb)
ON CONFLICT (event_name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Erasure tombstone
-- ---------------------------------------------------------------------------

-- Raw rows and sessions are deleted; aggregates survive because they no longer
-- identify a reader. The mapping row keeps an erased_at marker when it exists.
CREATE OR REPLACE FUNCTION analytics_tombstone(p_analytics_id uuid)
RETURNS integer
LANGUAGE plpgsql
AS $$
DECLARE
    removed integer := 0;
    touched integer := 0;
BEGIN
    DELETE FROM analytics_events WHERE analytics_id = p_analytics_id;
    GET DIAGNOSTICS removed = ROW_COUNT;

    DELETE FROM analytics_sessions WHERE analytics_id = p_analytics_id;
    DELETE FROM app_opens WHERE analytics_id = p_analytics_id;

    UPDATE analytics_id_map
    SET erased_at = COALESCE(erased_at, now())
    WHERE analytics_id = p_analytics_id;
    GET DIAGNOSTICS touched = ROW_COUNT;

    RETURN removed + touched;
END;
$$;

-- Account deletion removes the mapping row; the trigger covers the analytics
-- rows in the same transaction so no separate call site is needed.
CREATE OR REPLACE FUNCTION analytics_id_map_tombstone()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.analytics_id IS NOT NULL THEN
        PERFORM analytics_tombstone(OLD.analytics_id);
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS analytics_id_map_tombstone_trg ON analytics_id_map;
CREATE TRIGGER analytics_id_map_tombstone_trg
    AFTER DELETE ON analytics_id_map
    FOR EACH ROW
    EXECUTE FUNCTION analytics_id_map_tombstone();
