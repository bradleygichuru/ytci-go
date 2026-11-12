-- Collaborative itinerary foundation: members, invites, plan revisions, audit events,
-- domain-scoped versions, and soft delete.

ALTER TABLE itineraries RENAME COLUMN version TO plan_version;
UPDATE itineraries SET plan_version = 1 WHERE plan_version IS NULL;
ALTER TABLE itineraries ALTER COLUMN plan_version SET DEFAULT 1;
ALTER TABLE itineraries ALTER COLUMN plan_version SET NOT NULL;

ALTER TABLE itineraries ADD COLUMN budget_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE itineraries ADD COLUMN members_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE itineraries ADD COLUMN deleted_at TIMESTAMP;

CREATE TABLE itinerary_members (
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    invited_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    joined_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (itinerary_id, user_id)
);

CREATE INDEX itinerary_members_user_idx ON itinerary_members (user_id);
CREATE UNIQUE INDEX itinerary_members_one_owner_idx ON itinerary_members (itinerary_id) WHERE role = 'owner';

-- Every existing itinerary keeps its current owner as an explicit member.
-- Orphaned itineraries with no matching user are left without an owner; the
-- account-deletion/ownership-transfer work handles those separately.
INSERT INTO itinerary_members (itinerary_id, user_id, role)
SELECT i.id, u.id, 'owner'
FROM itineraries i
JOIN users u ON u.id = i.user_id::text
WHERE i.user_id IS NOT NULL
ON CONFLICT DO NOTHING;

CREATE TABLE itinerary_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL CHECK (role IN ('editor', 'viewer')),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMP NOT NULL,
    max_uses INTEGER NOT NULL DEFAULT 5 CHECK (max_uses > 0),
    use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count >= 0 AND use_count <= max_uses),
    revoked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX itinerary_invites_itinerary_idx ON itinerary_invites (itinerary_id);
CREATE INDEX itinerary_invites_expiry_idx ON itinerary_invites (expires_at);

CREATE TABLE plan_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    snapshot JSONB NOT NULL,
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    reason TEXT NOT NULL DEFAULT 'save',
    checksum TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    UNIQUE (itinerary_id, version)
);

CREATE INDEX plan_revisions_itinerary_idx ON plan_revisions (itinerary_id, created_at DESC);

CREATE TABLE trip_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    itinerary_id UUID NOT NULL REFERENCES itineraries(id) ON DELETE CASCADE,
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    scope TEXT NOT NULL CHECK (scope IN ('plan', 'budget', 'members')),
    action TEXT NOT NULL,
    entity_type TEXT,
    entity_id TEXT,
    summary TEXT,
    before_json JSONB,
    after_json JSONB,
    revision_id UUID REFERENCES plan_revisions(id) ON DELETE SET NULL,
    request_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX trip_events_itinerary_created_idx ON trip_events (itinerary_id, created_at DESC);
CREATE INDEX trip_events_actor_idx ON trip_events (actor_id);
