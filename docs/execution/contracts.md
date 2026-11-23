# Execution contracts

Status: **frozen 1.0.0** — ticket [#359](https://github.com/bradleygichuru/ytci-mobile/issues/359)
Date: 2026-11-18
Owner: `stream:platform`
Applies to: MICE (#361–#364), recognition (#365–#368), species (#369–#372), analytics (#373–#375), shared shell (#360), verification (#376–#377)

This gate freezes the interfaces the four UI streams build against. It ships
schema, contracts, fixtures and tests — **not** feature behaviour. A stream may
start building the moment this branch is merged; it must not wait for the other
streams to finish their handlers.

## What is frozen

| Surface | Canonical file | Version |
|---|---|---|
| HTTP + JSON schemas | `api/openapi/contracts.v1.yaml` | 1.0.0 |
| Recognition events | `api/contracts/cloudevents.v1.yaml` + [cloudevents.md](cloudevents.md) | 1.0.0 |
| Roles and permissions | `api/contracts/roles.v1.yaml` | 1.0.0 |
| MICE vocabulary | `api/contracts/mice.v1.yaml` | 1.0.0 |
| Species vocabulary | `api/contracts/species.v1.yaml` | 1.0.0 |
| Analytics vocabulary | `api/contracts/analytics.v1.yaml` | 1.0.0 |
| Mock fixtures | `fixtures/` | 1.0.0 |
| Contract version registry | `platform_contract_versions` (migration 0069) | 1.0.0 |

The OpenAPI document has **120 operations**. The change-control test
`TestOpenAPIValid` pins every path and method; adding or removing one fails CI
until the freeze list is updated deliberately. Operations 112–120 are the MICE
admin additions documented in [mice-admin-ops.md](mice-admin-ops.md); they are
additive to 1.0.0 and change no existing operation.

### Operation groups

| Tag | Base paths | Stream |
|---|---|---|
| `mice` | `/v1/public/events*`, `/v1/public/organizers/*`, `/v1/public/venues/*`, `/v1/mobile/events/*`, `/v1/mobile/registrations`, `/v1/admin/venues*`, `/v1/admin/organizers*`, `/v1/admin/events*`, `/v1/admin/event-reports*` | MICE |
| `recognition` | `/v1/public/leaderboard`, `/v1/public/seasons/*`, `/v1/mobile/recognition*`, `/v1/admin/recognition/*` | Recognition |
| `species` | `/v1/public/species*`, `/v1/mobile/species*`, `/v1/admin/species*` | Species |
| `analytics` | `/v1/mobile/analytics/*`, `/v1/admin/analytics/*` | Analytics |
| `platform` | `/v1/public/contracts` | Platform |

Errors are frozen as:

```json
{ "error": { "code": "EVENT_CAPACITY_FULL", "message": "…", "details": {}, "requestId": "…" } }
```

Validation failures use `{ "errors": [ { "field": "…", "message": "…" } ] }`.
Cursor pages use `{ "items": [], "nextCursor": null, "hasMore": false }`.

## Migration ownership

Migrations live in `testdata/migrations/` and are applied in filename order on a
fresh database by `internal/db.SetupTestDB`. Ranges are reserved per stream so
two feature branches never invent the same number.

| Range | Owner stream | Tickets | Contract slice landed here |
|---|---|---|---|
| 0002–0016 | existing history | — | pre-gate |
| **0017–0029** | MICE | #361 #362 #363 #364 | `0017_mice_event_contracts.sql` |
| **0030–0042** | Recognition | #365 #366 #367 #368 | `0030_recognition_contracts.sql` |
| **0043–0055** | Species | #369 #370 #371 #372 | `0043_species_contracts.sql` |
| **0056–0068** | Analytics | #373 #374 #375 | `0056_analytics_contracts.sql` |
| **0069–0079** | Platform | #359 #360 #376 | `0069_platform_contracts.sql` |
| **0080–0089** | Verification | #377 | reserved |
| 0090+ | Platform emergency only | — | requires platform approval |

Rules:

1. **One migrations owner.** Only `stream:platform` merges files in
   `testdata/migrations/`. Feature branches submit SQL in their own range and a
   platform reviewer lands it.
2. **No stream edits another stream's tables.** Cross-stream reads go through
   the OpenAPI contract or an owning module, never a foreign table write.
3. **Additive first.** New columns/tables are additive. Destructive changes
   (drop, rename, narrow a check, change a type) need a contract change ticket.
4. **Migrations are replayed from scratch.** Every migration must apply on a
   fresh database and must not depend on a previous deployment's data.
5. **Legacy aliases are explicit.** `events.status = 'scheduled'` is accepted as
   a deprecated alias of `published` until #361 finishes the handler cutover.

### Schema ownership map

| Stream | Tables |
|---|---|
| MICE | `venues`, `venue_accessibility`, `organizer_profiles`, `organizer_verification_events`, `organizer_verification_evidence`, `organizer_staff`, `event_sessions`, `event_people`, `session_speakers`, `event_registrations`, `event_registration_questions`, `event_ticket_handoffs`, `event_tickets`, `event_checkins`, `event_checkin_scans`, `event_reports`, `event_notifications`, `event_change_notices`, `event_accessibility` |
| Recognition | schema `recognition.*`, plus `recognition.event_types`, `rules`, `badge_definitions`, `badge_awards`, `xp_entries`, `xp_totals`, `levels`, `progress_tracks`, `progress_snapshots`, `seasons`, `leaderboard_entries`, `leaderboard_snapshots`, `display_names`, `moderation_cases`, `leaderboard_reports`, `disqualifications`, `retractions`, `consents`, `kill_switches`, `outbox`, `backfill_batches`, `audit` |
| Species | `content_languages`, `species`, `species_names`, `species_content`, `species_media`, `species_media_alt`, `species_audio`, `species_sources`, `species_rights`, `species_where_to_see`, `species_links`, `species_reviews` |
| Analytics | `analytics_installs`, `analytics_id_map` (extended), `analytics_identity_access_log`, `analytics_sessions`, `analytics_events` (extended), `analytics_event_schemas`, `analytics_ingest_batches`, `analytics_consents`, `analytics_daily`, `analytics_journey_daily`, `analytics_retention_cohorts`, `analytics_rollup_runs`, `analytics_erasure_requests`, `analytics_erasure_events`, `analytics_service_diagnostics`, `app_opens` (extended) |
| Platform | `platform_contract_versions`, `users_role_check` |

## Role model

`api/contracts/roles.v1.yaml` is the single source of truth. The server loads it
through `internal/contracts`; `internal/middleware` derives `IsAdmin` and the
Then & Now gates from it, so there is no second role table in code.

- **Roles:** `user`, `county_officer`, `content_editor`, `species_expert`,
  `translator`, `language_reviewer`, `rights_reviewer`, `moderator`,
  `organizer`, `administrator`, `super_admin`.
- **Publisher is a permission, not a role.** `species.publish`,
  `events.publish`, and `recognition.publish_*` are held by `administrator` and
  `super_admin` only.
- **Scanner is an assignment, not a role.** `organizer_staff.staff_role =
  'scanner'` grants `events.scan` for one organizer.
- **Reviewer ≠ uploader.** `media_uploader` and `rights_reviewer` cannot be held
  by the same actor; `translator` and `language_reviewer` cannot either.
- **No self-approval.** `actor_separations` forbid the same actor from
  publishing their own recognition rule, verifying the organizer they
  submitted, approving their own backfill, or approving their own XP adjustment.
- **XP adjustment second approver.** `POST /v1/admin/recognition/adjustments`
  carries the approver in the required `X-Second-Approver` header (an additive
  operation parameter; the request body is unchanged). The named user id must
  exist, be active (not banned), differ from the authenticated caller, and hold
  `recognition.approve_adjustment` (`super_admin`). Both actors are written to
  `recognition.audit` — `xp.adjust` for the requester and `xp.approve` for the
  approver. The header is documented in the frozen OpenAPI operation and is
  pending change-control sign-off; see ytci-go#47.
- **Recognition RBAC:** authors draft and simulate; administrators publish;
  super-admins reverse, retract, disqualify, close seasons, and flip kill
  switches. Moderators are read-only.

The contract test `TestRolePermissionInvariants` and
`TestActorSeparations` fail if any of these invariants regress.

## Fixtures and mock server

```bash
go run ./cmd/contracts-mock -addr :8099
```

- Serves every route in `fixtures/routes.go`, which must match the OpenAPI
  document.
- `?state=default|empty|error|stale` or `X-Fixture-State` selects a state.
- `error` fixtures return **503** with the standard error envelope.
- `stale` fixtures return **200** plus `X-Data-Stale: true`.
- A route without the requested state falls back to `default` and sets
  `X-Fixture-State-Fallback`.
- `GET /__fixtures` lists routes, fixture bases and available states.
- `GET /health` reports the contract version.

Primary collection routes ship all four states:

| Track | Primary fixtures |
|---|---|
| MICE | `mice/events` |
| Recognition | `recognition/overview`, `recognition/badges`, `recognition/leaderboard` |
| Species | `species/list` |
| Analytics | `analytics/overview` |

Every other route ships a `default` fixture; detail routes also ship `error`
and, where meaningful, `stale`.

## Contract tests

```bash
go test ./internal/contracts -count=1
go test ./cmd/contracts-mock -count=1
go test ./... -count=1
```

The suite covers:

- OpenAPI parses and validates; every operation is frozen by path/method.
- CloudEvents names, occurrence keys, award key and payload allow-list.
- Roles, permissions and separation-of-duty invariants.
- Stream vocabularies match the OpenAPI enums.
- Every fixture validates against its operation's schema.
- Every OpenAPI JSON operation has a mock route and vice versa.
- The full migration directory applies to a fresh PostGIS database, with
  constraint-level assertions for the ledger, role check, event lifecycle,
  organizer verification, analytics identity and registration-question cap.

CI: `.github/workflows/contracts.yml`.

## Typed clients

The OpenAPI document is the generation input. Clients are generated in the
consumer repositories and committed by the owning stream:

```bash
# Mobile (TypeScript) — owned by #360
npx --yes openapi-typescript@7 api/openapi/contracts.v1.yaml \
  -o ../ytci-mobile/lib/api/generated/contracts.ts

# Optional Go client for internal tooling
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest \
  -package contractsapi \
  -generate types,client \
  api/openapi/contracts.v1.yaml > internal/contracts/gen/client.gen.go
```

`make contracts-clients` prints the same commands. Generated clients are
additive artifacts; hand edits are rejected in review. The mobile shell (#360)
owns committing the TypeScript output and the `go-contract` test that calls the
mock server.

## Change control

1. **Additive, non-breaking** (new optional field, new path, new enum value
   that clients already tolerate): platform review + version bump in
   `platform_contract_versions` + fixture + test. No new ADR needed.
2. **Breaking** (rename/remove field, change type, change required-ness, change
   an enum meaning, change an award/idempotency key): a **change-control ticket**
   is required before code. It must include the migration path, a version bump
   (major), updated OpenAPI + fixtures, a deprecation window for mobile, and an
   ADR amendment when a product decision changes.
3. **Freeze list changes** are deliberate: `frozenOperations` in
   `internal/contracts/spec_test.go` and the operation count both change in the
   same PR as the contract ticket.
4. **No feature stream merges schema directly.** See migration ownership above.
5. **No silent renames.** Deprecated fields stay readable for at least one app
   release and are marked `deprecated: true` in OpenAPI.

## What this slice deliberately leaves to feature streams

- Handlers, validation beyond contract tests, and business rules.
- Push delivery for event notifications (reuse the existing Program channel).
- Outbox workers, rule evaluation, rollup jobs and reconciliation.
- Real content: badge names, mission catalogue, species prose, sources and
  rights records.
- Mobile screens, admin screens and copy.
- The Then & Now footer work; its permissions moved into `roles.v1.yaml` but
  behaviour is unchanged.
