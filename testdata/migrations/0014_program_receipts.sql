ALTER TABLE program_update_deliveries
  ADD COLUMN IF NOT EXISTS expo_receipt_status text,
  ADD COLUMN IF NOT EXISTS receipt_checked_at timestamptz;
CREATE INDEX IF NOT EXISTS program_update_deliveries_receipt_due_idx
  ON program_update_deliveries (sent_at)
  WHERE expo_ticket_id IS NOT NULL AND receipt_checked_at IS NULL;
