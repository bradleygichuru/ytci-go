-- MICE venue evidence, retirement/merge and organizer evidence requests
-- (additive).
--
-- The admin surface used to keep two jobs in browser storage: venue evidence
-- (per-claim references) and organizer "request more evidence" notes. Both now
-- have server records so another operator can see them and so an audit row
-- survives a cleared browser.
--
-- The venue columns are additive: retired_at is NULL for every existing venue,
-- so nothing changes for the public read until an admin retires one. A merged
-- venue keeps its row (and its evidence) and points at the surviving record.

ALTER TABLE venues
    ADD COLUMN IF NOT EXISTS retired_at timestamptz;
ALTER TABLE venues
    ADD COLUMN IF NOT EXISTS retired_reason text;
ALTER TABLE venues
    ADD COLUMN IF NOT EXISTS merged_into_venue_id uuid
        REFERENCES venues(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS venues_retired_idx
    ON venues (retired_at DESC)
    WHERE retired_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Venue evidence. One row per claim: what was claimed, the operator's note and
-- an optional reference (site report, photo, inspection ticket). Evidence is
-- append-only; correcting a claim records a new row.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS venue_evidence (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id    uuid NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    claim       text NOT NULL,
    note        text NOT NULL DEFAULT '',
    reference   text,
    recorded_by text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS venue_evidence_venue_idx
    ON venue_evidence (venue_id, created_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- Organizer evidence requests. A request keeps the verification record pending
-- and is resolved when a verification decision is recorded.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS organizer_evidence_requests (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id  uuid NOT NULL REFERENCES organizer_profiles(id) ON DELETE CASCADE,
    note          text NOT NULL,
    requested_by  text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at   timestamptz
);

CREATE INDEX IF NOT EXISTS organizer_evidence_requests_organizer_idx
    ON organizer_evidence_requests (organizer_id, created_at DESC, id DESC);
