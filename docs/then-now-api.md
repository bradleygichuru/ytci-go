# Then & Now content and authoring API

Status: v1 content/authoring slice plus dark-launched privacy controls. V1 visits stay local-only; server-side recognition, location verification, XP, badges, passports, and leaderboards remain gated by the privacy enablement decision (#290 in `ytci-mobile`) and are off by default.

## Auth

- `GET /v1/mobile/then-now/*` and `GET /v1/public/then-now/*` are public reads.
- `/v1/then-now/*` requires a session token or JWT and the `ThenNowGate`.
- Roles:
  - `content_editor`: read, create, edit, submit.
  - `administrator`: content editor actions plus review, assign, rights/client approval, publish, unpublish, schedule.
  - `super_admin`: administrator actions plus archive and revoke.
  - `moderator`: read-only.
  - `county_officer` / `user`: no access.

## Model

An experience separates its **live lifecycle** from its **open revision**:

- `experience.status`: `draft`, `in_review`, `approved`, `scheduled`, `published`, `unpublished`, `archived`, `revoked`.
- `openRevision.status`: `draft`, `in_review`, `approved`, `published`.
- `liveRevisionNumber` and `openRevisionNumber` identify revisions. A published experience keeps `status = published` while an open revision is drafted, submitted, or approved. The client reads `openRevision.status` for the working revision.
- Submitting freezes the open revision; approving does not publish; publishing promotes the open revision to live.
- The approved admin flow publishes the **experience first**, then the **site**, because the site readiness gate requires a published experience. The site page is not publicly readable until the site publish step.

### Revision snapshot

`openRevision.snapshot` is the immutable revision payload:

```json
{
  "title": "The railway that built Nairobi",
  "promise": "See the museum yard as it is today.",
  "story": "...",
  "period": "1899–1971",
  "duration": 77,
  "contentNotes": "...",
  "safetyNotes": "...",
  "sources": "...",
  "factCheck": "...",
  "primaryMode": "camera_video",
  "languages": ["EN", "SW"],
  "rightsRecord": "Client footage · cleared",
  "rightsEvidence": "Signed release",
  "rightsExpiry": "2027-06-30",
  "consentRecord": "Community review recorded",
  "sensitivity": "standard",
  "videoMaster": "then-now/railway/master.mp4",
  "videoSize": "1.4 GB",
  "renditions": { "1080": "Ready", "720": "Ready", "540": "Ready" },
  "renditionKeys": { "1080": "then-now/railway/1080.mp4", "720": "...", "540": "..." },
  "poster": true,
  "posterKey": "then-now/railway/poster.jpg",
  "captions": { "en": true, "sw": true },
  "captionKeys": { "en": "then-now/railway/en.vtt", "sw": "..." },
  "transcript": true,
  "transcriptKey": "then-now/railway/transcript.txt",
  "audioDescription": true,
  "audioDescriptionKey": "then-now/railway/ad.vtt",
  "textCards": [{ "at": 6, "text": "..." }, { "at": 22, "text": "..." }]
}
```

Readiness requires the 1080p rendition key, poster key, English caption key, transcript key, and at least two text cards. Warnings cover Swahili captions, audio description, and the 540p rendition. Rights expiry must not be in the past.

## Admin endpoints

All paths are under `/v1/then-now`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/sites` | List sites. Filters: `county`, `accessMode`, `sensitivity`, `status`, `q`. |
| POST | `/sites` | Create a site draft. |
| GET | `/sites/{id}` | Site detail, experiences, and site readiness. |
| PATCH | `/sites/{id}` | Partial site update. |
| POST | `/sites/{id}/publish` | Publish after site readiness clears. |
| POST | `/sites/{id}/unpublish` | Hide the public site. |
| POST | `/sites/{id}/archive` | Super-admin archive. |
| GET | `/experiences` | List experiences. Filters: `siteId`, `status`, `mode`, `review`, `language`, `q`. |
| POST | `/experiences` | Create a draft experience and revision 1. |
| GET | `/experiences/{id}` | Experience, open/live revisions, site, readiness. |
| PATCH | `/experiences/{id}` | Update the open draft revision snapshot/title. |
| POST | `/experiences/{id}/submit` | Freeze the open revision for review. |
| POST | `/experiences/{id}/assign` | Assign an administrator reviewer; defaults to the caller. |
| POST | `/experiences/{id}/decisions` | `approve`, `request_changes`, `reject`, `return`. Notes required for all but approve. |
| POST | `/experiences/{id}/client-approval` | Record a client approver without an account. |
| POST | `/experiences/{id}/external-reviewer` | Record an external reviewer decision. |
| POST | `/experiences/{id}/schedule` | Schedule an approved revision. Body `{scheduledAt: RFC3339, note}`. |
| POST | `/experiences/{id}/publish` | Promote an approved revision to live. |
| POST | `/experiences/{id}/new-revision` | Clone the live revision into a new draft. |
| POST | `/experiences/{id}/status` | `{action: "unpublish" \| "archive" \| "revoke"}`. |
| GET | `/review?filter=all\|unassigned\|mine` | Review queue. |
| GET | `/review/{revisionId}` | Frozen review detail. |
| GET | `/audit?entityType=&entityId=` | Recent audit events. |

### Create site

```json
{
  "name": "Nairobi Railway Museum",
  "slug": "nairobi-railway-museum",
  "county": "Nairobi",
  "locality": "Nairobi",
  "siteType": "historical",
  "accessMode": "camera_video",
  "sensitivity": "standard",
  "managingAuthority": "National Museums of Kenya",
  "photographyPolicy": "Allowed with attribution",
  "culturalAuthority": "National Museums of Kenya",
  "consentRecord": "Community review recorded",
  "consentDate": "2026-09-12",
  "siteRules": "Stay behind the marked line.",
  "safetyNotes": "The yard is uneven.",
  "shortDescription": "The railway that built Nairobi.",
  "fullDescription": "A current-scene portrait.",
  "sources": "NMK labels",
  "factCheck": "Signed off",
  "publicRightsLine": "Footage used with permission.",
  "latitude": -1.293613,
  "longitude": 36.822641,
  "geofenceRadiusM": 150,
  "standingPointNote": "Main yard entrance.",
  "imageKey": "then-now/railway.jpg"
}
```

### Create experience

```json
{
  "siteId": "<site uuid>",
  "title": "The railway that built Nairobi",
  "snapshot": { "...": "the revision snapshot above" }
}
```

`PATCH /experiences/{id}` accepts `{ "title": "...", "snapshot": { ... } }`. The snapshot is replaced, so send the full object.

### Decisions

```json
{ "decision": "request_changes", "notes": "Shorten card 02." }
```

Sensitive revisions require `secondReviewerApproved` before approve. Restricted revisions cannot be approved. A reviewer cannot be the revision creator or submitter.

## Mobile and public reads

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/mobile/then-now/sites` | Published sites that have a published experience. |
| GET | `/v1/mobile/then-now/sites/{slug}` | Published site + primary published experience. |
| GET | `/v1/public/then-now/sites` | Same, unauthenticated. |
| GET | `/v1/public/then-now/sites/{slug}` | Same, unauthenticated. |

The public payload returns the site's public identity, access mode, county/locality, coordinates/geofence, rights line, site rules, safety notes, and the primary experience's title/promise/story/period/duration, languages, text cards, video/rendition/poster/caption/transcript keys, and accessibility flags. Internal rights evidence, sources, fact-check, and consent stay off the public payload.

## Scheduled publishing

`cmd/server` starts `thennow.StartScheduler` every minute. It promotes approved revisions whose `scheduledAt` has passed, sets the live revision atomically, and writes an audit event. If the revision's rights expiry has passed, the scheduler clears the schedule and unpublishes the live experience instead of promoting it.

## Seed

```sh
DATABASE_URL=... go run ./cmd/seed-then-now
DATABASE_URL=... make verify-then-now-seed
```

The seed is idempotent and inserts the four illustrative sites (Railway Museum, War Cemetery, African Heritage House, Nabongo Cultural Centre). All four are published with live experiences; the two cultural sites use `not_available` access and carry a permission-pending public rights line. The photos/object keys are illustrative; replace them with client-cleared assets before launch.

## Tests

```sh
go test ./... -short          # unit tests, no Docker
go test ./...                 # integration tests, testcontainers/PostGIS
```

The integration tests cover authoring → submit → assign → approve → publish, sensitive second-reviewer gating, reviewer ≠ uploader, content-editor/moderator RBAC, published-only mobile/public reads, audit, and the idempotent seed.

## Privacy controls

Migration `0014_then_now_privacy.sql` adds consent versioning, pseudonymous analytics, and anonymous heritage counts. The recognition feature remains dark; v1 visits stay local to the device. See the mobile repo's [recognition privacy requirements](https://github.com/bradleygichuru/ytci-mobile/blob/docs/then-now-privacy/docs/privacy/then-now-recognition.md).

### Feature flags

| Env | Default | Purpose |
| --- | --- | --- |
| `THEN_NOW_RECOGNITION_ENABLED` | `false` | Server-side recognition; not enabled until the privacy gate is signed |
| `THEN_NOW_PUBLIC_RECOGNITION_ENABLED` | `false` | Public passport/leaderboard |
| `THEN_NOW_ANALYTICS_V2_ENABLED` | `false` | Pseudonymous analytics and analytics consent enforcement |

`GET /v1/public/then-now/config` and `GET /v1/mobile/then-now/config` return these flags for the mobile UI. The server still enforces them; hiding the UI is not a control.

### Consent

`GET /v1/mobile/profile/consent` now returns:

```json
{
  "location": false,
  "camera": false,
  "notifications": false,
  "ugc": false,
  "recognition": false,
  "locationVerification": false,
  "publicProfile": false,
  "leaderboard": false,
  "analytics": false,
  "version": "2026-11-18",
  "updatedAt": "...",
  "consentGrantedAt": "...",
  "consentWithdrawnAt": "..."
}
```

`PATCH /v1/mobile/profile/consent` accepts any of the boolean fields plus an optional `version`. The server records every change in `consent_records`, sets `consent_granted_at` on the first grant, and sets `consent_withdrawn_at` when recognition is turned off. Recognition requires an age range; `Under 18` is rejected with `AGE_RESTRICTED`. Public profile, leaderboard, and location verification require recognition consent.

### Analytics

- With `THEN_NOW_ANALYTICS_V2_ENABLED=true`, app opens and events require analytics consent and are written with a random `analytics_id` from `analytics_id_map`, not the account `user_id`. With consent off, the endpoints return `{"status":"ignored"}` and write nothing.
- The account deletion removes the analytics mapping, clears old `user_id` rows, anonymises consent history, and leaves pseudonymous analytics rows. Export/retention follow the privacy spec.
- Admin DAU/WAU/MAU counts distinct `COALESCE(analytics_id::text, user_id::text)` so old and new rows can coexist during rollout.
- Before switching `THEN_NOW_ANALYTICS_V2_ENABLED` on, run the one-time backfill once:
  `go run ./cmd/backfill-analytics --apply`. It is a dry run by default, is idempotent, creates a persistent `analytics_id_map` entry for each existing user with historical rows, moves those rows to the random analytics ID, and clears `user_id`. Rows whose `user_id` no longer exists are reported and left for the separate data-cleanup decision.
- The retention worker runs daily in `cmd/server`: app opens and analytics events 14 months, audit logs 12 months, anonymous heritage counts 24 months.

### Anonymous heritage counts

`POST /v1/public/then-now/events` (and the mobile equivalent) accepts:

```json
{ "event": "portal_opened", "siteSlug": "nairobi-railway-museum" }
```

Allowed events: `portal_opened`, `video_played`, `visit_marked`, `verified_visit`. The endpoint resolves a published site, stores a daily count, and stores sensitive/restricted sites only as a combined `scope='sensitive'` bucket with no site ID. It never accepts or stores a reader, device, or location.

The public write is rate-limited per client IP with a 20-event burst and a one-event-per-second refill, on top of the general public limit. A rejected request returns `429` with `Retry-After: 60` and code `RATE_LIMITED`; the client may retry later, but no count is written on rejection.

`GET /v1/analytics/heritage` (admin) returns the last 30 days of counts, suppressing any cell below 5. Sensitive rows show `Sensitive sites (combined)` and no site identity.

### Privacy export

`GET /v1/mobile/privacy/export` returns the reader's consent state and history, existing destination visits, badges, the recognition status (`local_only` in v1), and the analytics model. It never includes passwords, tokens, analytics event streams, or other readers' data. The mobile client merges the device-local heritage visited list into the same JSON before sharing it.

## Known follow-ups

- Real R2 uploads remain on the existing media presign/complete pipeline; this API stores object keys.
- `testdata/migrations/0013_then_now.sql` and `0014_then_now_privacy.sql` mirror the schema for Go integration tests only. Mirror the same DDL into TanStack admin Drizzle migrations before wiring the admin.
- The admin UI fixture store still needs wiring to these endpoints.
- The recognition privacy decision is resolved; server-side visits, verification, XP, badges, passports, and leaderboards remain gated and are not part of v1. V1 visits stay local-only.
