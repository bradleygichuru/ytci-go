-- Platform contract gate (ticket #359, migration range 0069-0079).
-- Owner: stream:platform (#359, #360, #376).
-- Records the frozen contract versions and constrains the actor role column to
-- the single role model in api/contracts/roles.v1.yaml.

CREATE TABLE IF NOT EXISTS platform_contract_versions (
    contract      text PRIMARY KEY,
    version       text NOT NULL,
    frozen_at     timestamptz NOT NULL DEFAULT now(),
    owner_stream  text NOT NULL,
    notes         text
);

INSERT INTO platform_contract_versions (contract, version, owner_stream, notes)
VALUES
    ('openapi',      '1.0.0', 'platform',    'api/openapi/contracts.v1.yaml'),
    ('cloudevents',  '1.0.0', 'recognition', 'api/contracts/cloudevents.v1.yaml'),
    ('roles',        '1.0.0', 'platform',    'api/contracts/roles.v1.yaml'),
    ('mice',         '1.0.0', 'mice',        'api/contracts/mice.v1.yaml'),
    ('species',      '1.0.0', 'species',     'api/contracts/species.v1.yaml'),
    ('analytics',    '1.0.0', 'analytics',   'api/contracts/analytics.v1.yaml')
ON CONFLICT (contract) DO NOTHING;

-- Canonical actor roles. Per-object authority (organizer scoping, scanner
-- assignment, recognition publish) is carried by the permission matrix in
-- api/contracts/roles.v1.yaml, not by additional role names.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_role_check') THEN
        ALTER TABLE users ADD CONSTRAINT users_role_check
            CHECK (role IS NULL OR role IN (
                'user',
                'moderator',
                'county_officer',
                'content_editor',
                'species_expert',
                'translator',
                'language_reviewer',
                'rights_reviewer',
                'organizer',
                'administrator',
                'super_admin'
            ))
            NOT VALID;
    END IF;
END $$;

-- Recognition writes must never be self-approved. The ledger already requires
-- an approver for adjustments; the backfill table carries the same CHECK.
