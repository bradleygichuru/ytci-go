-- Competition self-entry (map #18, task #41).
--
-- Additive only. Entrants now create their own competition entry from a
-- published repository entry, so the API needs to record guardian consent and
-- a soft withdrawal. Withdrawal must free the category and the work for a
-- later re-entry, so the two entrant-facing unique indexes become partial.
--
-- Mirror this in the admin Drizzle migrations before deploying. The Go test
-- schema is the executable copy of the same change.

ALTER TABLE competition_entries
    ADD COLUMN IF NOT EXISTS guardian_consent boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS guardian_consent_at timestamp,
    ADD COLUMN IF NOT EXISTS withdrawn_at timestamp,
    ADD COLUMN IF NOT EXISTS withdrawn_by text;

-- The old full unique indexes keep a withdrawn row blocking a re-entry.
DROP INDEX IF EXISTS competition_entries_edition_category_lead_idx;
DROP INDEX IF EXISTS competition_entries_edition_category_repository_idx;

CREATE UNIQUE INDEX IF NOT EXISTS competition_entries_edition_category_lead_active_idx
    ON competition_entries (edition_id, category_id, lead_user_id)
    WHERE withdrawn_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS competition_entries_edition_category_repository_active_idx
    ON competition_entries (edition_id, category_id, repository_entry_id)
    WHERE withdrawn_at IS NULL;

-- Listing an entrant's live entries and counting their categories.
CREATE INDEX IF NOT EXISTS competition_entries_lead_edition_active_idx
    ON competition_entries (lead_user_id, edition_id)
    WHERE withdrawn_at IS NULL;
