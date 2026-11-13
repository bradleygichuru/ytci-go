-- Idempotency keys for offline budget writes.
--
-- Mobile clients retry queued writes, so each client-generated request id may
-- only create one expense or receipt per itinerary.

ALTER TABLE expenses ADD COLUMN client_request_id TEXT;
CREATE UNIQUE INDEX expenses_client_request_idx
    ON expenses (itinerary_id, client_request_id)
    WHERE client_request_id IS NOT NULL;

ALTER TABLE expense_receipts ADD COLUMN client_request_id TEXT;
CREATE UNIQUE INDEX expense_receipts_client_request_idx
    ON expense_receipts (itinerary_id, client_request_id)
    WHERE client_request_id IS NOT NULL;
