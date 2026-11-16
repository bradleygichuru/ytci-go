# Alerts backend

The alerts domain is additive to the existing campaign push path. It ingests official CAP/travel-advice sources, maps them to Kenyan counties, matches user preferences and area context, and sends Expo pushes through a separate worker.

## Components

- `internal/alerts/cap.go` — CAP 1.2 XML and KMD CAP RSS parsing.
- `internal/alerts/fcdo.go` — FCDO travel-advice JSON and safety-section hash.
- `internal/alerts/mapping.go` — `areaDesc` county/region parsing and polygon WKT conversion.
- `internal/alerts/mapper.go` — exact county text, regional text + polygon union, review flags.
- `internal/alerts/policy.go` — severity/category gates and quiet-hours scheduling.
- `internal/alerts/store.go` — alert upsert, idempotency ledger, areas, update history, fan-out jobs.
- `internal/alerts/fanout.go` — matching, delivery scheduling, Expo send, receipts, token deactivation.
- `internal/handler/expo/alerts.go` — mobile/public alert and preference endpoints.
- `internal/handler/admin/alerts.go` — admin Program Update create/list/publish/expire and source/job health.
- `cmd/alerts/main.go` — `ingest` cron binary and `worker` binary.

## Run

```bash
go build -o bin/alerts ./cmd/alerts
DATABASE_URL=... ./bin/alerts ingest
DATABASE_URL=... EXPO_PUSH_TOKEN=... ./bin/alerts worker
```

`ALERTS_KMD_RSS_URL` and `ALERTS_FCDO_URL` override the source URLs; `ALERTS_DRY_RUN=true` logs deliveries without calling Expo.

## Data prerequisites

`kenya_counties` must contain the 47 OCHA COD-AB ADM1 rows before ingest. If it is empty, `NewIngester` fails closed with `ErrCountiesEmpty`; it never pushes to nobody.

## Endpoints

Optional-auth mobile (guests included; ADR-0004 keeps read routes on `/v1/mobile` rather than duplicating them under `/v1/public`):
- `GET /v1/mobile/alerts` — every active public alert plus 30-day expired/cancelled tombstones. Push preferences never hide the inbox; use `?countyGid=` for the county filter. The response includes `sourceHealth` (`ok`/`degraded`/`unavailable`/`unknown`).
- `GET /v1/mobile/alerts/{id}`
- `GET /v1/mobile/program-updates` — global updates plus county-targeted updates for the signed-in user
- `GET /v1/mobile/alert-counties` — the 47 `kenya_counties` rows (`gid`, `name`, `pCode`) for home-county and watchlist pickers

`alert_preferences` exposes `enabled` (push opt-in), `osPermissionOk`, `minSeverity`, the opt-in
`categories` array (`Met`, `Safety`, `Security`, `Program`), `programImportantOnly`, quiet hours,
home/watch counties, `followCurrentLocation`, and language. Push payload `data` carries only
`alertId`, `severity`, `sourceId`, and `countyGid`.

Authenticated mobile:
- `POST /v1/mobile/alerts/{id}/read`
- `POST /v1/mobile/alerts/{id}/dismiss`
- `GET/PATCH /v1/mobile/alert-preferences`
- `POST /v1/mobile/alert-location` (`{ "countyGid": 22 }`)
- `POST /v1/mobile/program-updates/{id}/read`

Admin:
- `GET/POST /v1/admin/program-updates`
- `POST /v1/admin/program-updates/{id}/publish`
- `POST /v1/admin/program-updates/{id}/expire`
- `GET /v1/admin/alerts/health`

## Deliberate v1 limits

- FCDO is stored as a national advisory and is **not pushed** (`alert_sources.push_enabled=false`).
- Quiet-hours suppression is decided at fan-out time and the delivery is left `skipped_quiet_hours`; per #242 there is no delayed morning push. The inbox remains the record.
- Non-Extreme pushes are capped at 4/day and 2/hour per user; capped deliveries are recorded as `skipped_preference` and remain in the inbox.
- The client-reported OS permission (`osPermissionOk`) gates push and defaults to `false` (fail closed). The mobile client must PATCH it to `true` after the OS prompt; legal consent is still `user_profiles.consent_notifications`.
- Token registration creates an `alert_preferences` row and seeds the home county from `user_profiles.county` when it matches a county; the schema migration backfills existing consenting users.
- New accounts default to Weather (`Met`) push only at Severe+, with Security, Safety, and Program off. Quiet hours are 22:00–06:00; Extreme always bypasses and Severe overnight is opt-in (`criticalOverride`).
- Push eligibility requires a non-null `expires_at`; alerts without an expiry stay inbox-only. A cancellation is push-eligible even without an expiry because it targets readers who already received the original.
- `alert_sources.push_enabled` gates KMD fan-out; FCDO remains in-app only.
- Campaign sends now create a Program Update and use the Program audience. `push_notifications.target_audience` accepts JSON: `{"countyGids":[1,2],"interests":["wildlife"],"language":"en","guests":false,"important":false}`. Guests only receive global campaigns in v1.
- Program Update publish sends immediately through the Program channel (`program_updates`) with quiet-hours and cap suppression; sent/skipped rows are recorded in `program_update_deliveries`. Program Expo receipts are not polled yet.
- The ingester persists `ETag` and `Last-Modified` on `alert_sources` and sends conditional requests; KMD's RSS may ignore them, so dedupe also relies on the CAP identifier and content hash.
- Failed polls back off exponentially from the source interval, capped at 30 minutes.
- When every item in a full KMD feed is new after a prior success, the run is marked `partial` with `possible_gap` so the inbox can show the source as degraded rather than implying an all-clear.
- Guest push token registration is schema-ready (`push_tokens.user_id` nullable, `installation_id`) but the public registration/fan-out path is left for the mobile slice.
- The production schema migrations live on the TanStack branch `feat/alerts-schema` (`0018_alerts.sql` through `0025_text_user_ids.sql`, renumbered after the marketplace `0017` migration from #29); the backend test schema is `testdata/migrations/0013_alerts.sql`.
- Better Auth user IDs are text (not UUIDs). `push_tokens.user_id`, `push_notifications.sent_by`, and `campaigns.created_by` are migrated to `text`; do not add UUID casts for them.
- Guest installation registration/fan-out remains deferred; guest tokens are schema-ready and receive global campaigns only.

## Tests

```bash
go test ./internal/alerts ./internal/handler/expo ./internal/handler/admin -count=1
```

The integration tests use a PostGIS testcontainer and cover migration application, CAP ingest end-to-end, upsert/idempotency/material updates, fan-out preference matching, alert list/read/preferences, and admin Program Update create/publish/list.
