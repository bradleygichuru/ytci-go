# Then & Now content and authoring API

Status: v1 content/authoring slice. Visited records, location verification, XP, badges, passports, leaderboards, trails, recognition, 360°, and anchors are out of scope and wait on the privacy/consent/retention grill (#290 in `ytci-mobile`).

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
```

The seed is idempotent and inserts the four illustrative sites (Railway Museum, War Cemetery, African Heritage House, Nabongo Cultural Centre). The photos/object keys are illustrative; replace them with client-cleared assets before launch.

## Tests

```sh
go test ./... -short          # unit tests, no Docker
go test ./...                 # integration tests, testcontainers/PostGIS
```

The integration tests cover authoring → submit → assign → approve → publish, sensitive second-reviewer gating, reviewer ≠ uploader, content-editor/moderator RBAC, published-only mobile/public reads, audit, and the idempotent seed.

## Known follow-ups

- Real R2 uploads remain on the existing media presign/complete pipeline; this API stores object keys.
- `testdata/migrations/0013_then_now.sql` mirrors the schema for Go integration tests only. Mirror the same DDL into a TanStack admin Drizzle migration before wiring the admin.
- The admin UI fixture store still needs wiring to these endpoints.
- Visited-record and location-verification endpoints wait on the privacy/consent/retention decisions.
