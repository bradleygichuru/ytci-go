-- Budget ledger, participants, expenses, splits, settlements, and receipts.

ALTER TABLE itineraries ADD COLUMN budget_total_kes BIGINT
    CHECK (budget_total_kes IS NULL OR budget_total_kes >= 0);
ALTER TABLE itineraries ADD COLUMN plan_estimate JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE trip_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    display_name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('member', 'offline')),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    removed_at TIMESTAMP,
    UNIQUE (itinerary_id, user_id)
);

CREATE UNIQUE INDEX trip_participants_offline_name_idx
    ON trip_participants (itinerary_id, lower(display_name))
    WHERE user_id IS NULL AND removed_at IS NULL;

INSERT INTO trip_participants (itinerary_id, user_id, display_name, kind)
SELECT m.itinerary_id, m.user_id, COALESCE(u.name, 'Member'), 'member'
FROM itinerary_members m
LEFT JOIN users u ON u.id = m.user_id
ON CONFLICT (itinerary_id, user_id) DO NOTHING;

CREATE TABLE trip_budget_targets (
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK (category IN (
        'Transport', 'Accommodation', 'Food', 'Park & entry fees',
        'Activities', 'Shopping', 'Conservation', 'Other'
    )),
    target_kes BIGINT NOT NULL CHECK (target_kes >= 0),
    PRIMARY KEY (itinerary_id, category)
);

CREATE TABLE expenses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN (
        'Transport', 'Accommodation', 'Food', 'Park & entry fees',
        'Activities', 'Shopping', 'Conservation', 'Other'
    )),
    original_amount NUMERIC(14,2) NOT NULL CHECK (original_amount > 0),
    currency TEXT NOT NULL CHECK (currency IN ('KES', 'USD', 'EUR', 'GBP', 'TZS', 'UGX', 'ZAR')),
    fx_rate NUMERIC(14,6) NOT NULL CHECK (fx_rate > 0),
    amount_kes BIGINT NOT NULL CHECK (amount_kes > 0),
    expense_date DATE NOT NULL,
    payer_participant_id UUID NOT NULL REFERENCES trip_participants(id) ON DELETE RESTRICT,
    notes TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    deleted_at TIMESTAMP
);

CREATE INDEX expenses_itinerary_date_idx ON expenses (itinerary_id, expense_date DESC);
CREATE INDEX expenses_payer_idx ON expenses (payer_participant_id);
CREATE INDEX expenses_deleted_idx ON expenses (itinerary_id) WHERE deleted_at IS NOT NULL;

CREATE TABLE expense_shares (
    expense_id UUID NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    participant_id UUID NOT NULL REFERENCES trip_participants(id) ON DELETE RESTRICT,
    share_kes BIGINT NOT NULL CHECK (share_kes >= 0),
    share_percent NUMERIC(5,2),
    PRIMARY KEY (expense_id, participant_id)
);

CREATE INDEX expense_shares_participant_idx ON expense_shares (participant_id);

CREATE TABLE settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    from_participant_id UUID NOT NULL REFERENCES trip_participants(id) ON DELETE RESTRICT,
    to_participant_id UUID NOT NULL REFERENCES trip_participants(id) ON DELETE RESTRICT,
    amount_kes BIGINT NOT NULL CHECK (amount_kes > 0),
    settlement_date DATE NOT NULL,
    note TEXT,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    deleted_at TIMESTAMP,
    CHECK (from_participant_id <> to_participant_id)
);

CREATE INDEX settlements_itinerary_idx ON settlements (itinerary_id, settlement_date DESC);

CREATE TABLE expense_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    expense_id UUID NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL,
    content_type TEXT NOT NULL,
    file_size_bytes INTEGER NOT NULL CHECK (file_size_bytes > 0),
    status TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'processing', 'ready', 'failed', 'deleted')),
    ocr_status TEXT NOT NULL DEFAULT 'pending' CHECK (ocr_status IN ('pending', 'processing', 'ready', 'failed', 'skipped')),
    ocr_merchant TEXT,
    ocr_date DATE,
    ocr_total NUMERIC(14,2),
    ocr_currency TEXT,
    ocr_confidence NUMERIC(4,3),
    ocr_text TEXT,
    ocr_error TEXT,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    deleted_at TIMESTAMP
);

CREATE UNIQUE INDEX expense_receipts_one_active_idx
    ON expense_receipts (expense_id)
    WHERE deleted_at IS NULL;
CREATE INDEX expense_receipts_itinerary_idx ON expense_receipts (itinerary_id);
