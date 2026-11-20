# Repository API — implementation notes

Implements tickets #8 (public reads) and #9 (submissions, uploads, admin CRUD)
for the YTCI Digital Repository MVP. The shared contract lives in
`ytci-repository/docs/api/repository-contract.md`; this file records how the Go
service implements it, where the schema forced a difference, and what the
client must do.

Package: `internal/handler/repository`
Routes: `internal/server/server.go` (`mountPublicRoutes`, `mountAdminRoutes`)

## Endpoints

Public (`/v1/public/repository`, general public rate limit):

| Method | Path |
| --- | --- |
| GET | `/entries` — `kind`, `county`, `category`, `q`, `featured`, `cursor`, `limit` |
| GET | `/entries/{slug}` |
| GET | `/facets` |
| GET | `/stats` |
| GET | `/settings` |
| POST | `/uploads/presign` — ~10/min per IP |
| POST | `/submissions` — 5/hour per IP (burst 5) |

Admin (`/v1/admin/repository`, `JWTAuth` + platform `AdminGate` +
`repository.AdminGate` for `super_admin`, `administrator`, `moderator`):

| Method | Path |
| --- | --- |
| GET/POST | `/entries` |
| GET/PATCH/DELETE | `/entries/{id}` |
| POST | `/entries/{id}/approve` \| `publish` \| `unpublish` \| `feature` \| `reject` |
| GET/PATCH | `/settings` |
| POST | `/uploads/presign` |

`moderator` may read, approve, reject, publish, unpublish and feature, but
cannot create or delete entries (`403 FORBIDDEN`).

## Media

- All media is private R2. Public responses sign GET URLs with a 15-minute
  TTL and include `expiresAt`.
- `coverImage` is the contract object; `coverUrl` / `coverThumbUrl` are
  additive flat aliases. `coverThumbUrl` prefers `media_assets.thumbnail_key`
  and falls back to the cover object.
- `gallery` is ordered by `repository_entry_media.sort_order`, then media id.
- `document` includes `fileName`, `mimeType` (`application/pdf` by policy) and
  `sizeBytes`.
- Public presign writes a one-use `repository_upload_intents` row for a key
  under `repository/submissions/{uuid}/{safe-filename}`, TTL 30 minutes.
- On submission the server locks each intent (`FOR UPDATE`), HEADs the object,
  compares the content type and byte size to the intent, deletes the object on
  a mismatch, creates `media_assets` rows (`entity_type = 'repository_entry'`)
  and marks the intents consumed in the same transaction.
- Admin presign writes a `media_assets` row immediately (`status = 'ready'`)
  and returns its `mediaId` (additive field) so an entry can reference it via
  `coverMediaId` / `documentMediaId`.

## Deviations from the contract

1. **Admin presign return shape.** The contract says the admin presign is
   "same request/response" as public. It is, plus `mediaId`. There is no
   separate admin "complete" route for repository media, so the media row is
   created at presign time.
2. **Flat media aliases.** `coverUrl`, `coverThumbUrl`, `documentUrl` are
   additive to the contract's nested objects (contract §8 allows additive
   fields).
3. **Stats aliases.** Besides `publishedEntries`, `countiesCovered`,
   `featuredEntries`, `byKind` and `lastPublishedAt`, the response also carries
   `entries`, `counties`, `competitions`, `innovations`,
   `conservationResources`, `experiences` (ticket #8 wording).
4. **Document `mimeType`.** `media_assets` has no content-type column. Documents
   are PDF-only by policy, so the API reports `application/pdf`.
5. **Approve from `draft`.** The contract lists `submitted`/`rejected`; ticket
   #9 describes `draft → approved → published`, so `draft` is also accepted.
6. **Auth status codes.** Missing/invalid admin credentials return `401
   UNAUTHORIZED` (existing `JWTAuth` behaviour and contract §1). An
   authenticated role without repository access returns `403 FORBIDDEN`.
   Ticket #9's "unauthenticated admin calls return 403" is satisfied in spirit
   by the role case; the token case stays `401` to match the rest of the API.
7. **`county_officer` read.** The contract's RBAC table grants read access to
   `county_officer`, but the mandated platform `AdminGate`
   (`platform.admin_access`) excludes that role, so repository admin routes
   return `403` for it. Granting read would require a separate auth path.
8. **Turnstile.** `turnstileToken` is accepted and ignored until server keys
   are configured. Honeypot plus IP rate limiting are the launch controls.
9. **Guardian consent.** `under_18` requires `guardianConsent: true`. The flag
   is recorded as an internal moderation note at submission time; no durable
   `guardian_consent` column exists yet.

## Content rules

- Public DTOs never include `authorEmail`, `authorPhone`, `authorAgeBand`,
  `referenceCode`, `moderationNote`, `reviewedBy`, `source`, object keys, or
  the generated `search_vector`.
- `bodyMd` is stored verbatim. The public site must render it through a
  Markdown renderer that sanitises HTML (no raw HTML, no `javascript:` URLs).
  The API does not strip Markdown because the source of truth for rendering is
  the client.
- Search uses the generated `search_vector` with `websearch_to_tsquery` for
  queries of three or more characters and a substring fallback over
  title/summary/tags for shorter or stop-word-only queries.
- Public reads set `Cache-Control: public, s-maxage=60,
  stale-while-revalidate=300`. This is shorter than the 15-minute media TTL.

## Status lifecycle

```
draft ──approve──▶ approved ──publish──▶ published ──unpublish──▶ approved
  └────publish──────────────────────────────▲
submitted ──approve──▶ approved ──publish────┘
     └────reject (note required)──▶ rejected ──approve──▶ approved
```

- `approve` sets `reviewed_by`/`reviewed_at`; the note is optional.
- `publish` sets `published_at` (first publish only) and promotes the entry's
  `pending_review` media to `ready`.
- `unpublish` returns the entry to `approved` and keeps `published_at`.
- `reject` requires a 10–2000 character note.
- `PATCH` may only move `draft ↔ archived`; every other transition goes
  through an action route and invalid ones return `409
  INVALID_STATUS_TRANSITION`.
- `DELETE` is limited to `draft`/`archived`; media rows are unlinked
  (`entity_type = 'unlinked'`), R2 objects are not deleted.
