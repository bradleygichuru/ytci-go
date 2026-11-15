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
- `internal/handler/admin/alerts.go` — admin Program Update create/list/publish/expire.
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

Public:
- `GET /v1/public/alerts`
- `GET /v1/public/alerts/{id}`
- `GET /v1/public/program-updates`

Optional-auth mobile:
- `GET /v1/mobile/alerts`
- `GET /v1/mobile/alerts/{id}`
- `GET /v1/mobile/program-updates`

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

## Deliberate v1 limits

- FCDO is stored as a national advisory and is **not pushed** (`alert_sources.push_enabled=false`).
- Program Updates are in-app only in this slice; pushing Important updates belongs with the campaign-notification unification work (#248).
- KMD RSS has no conditional GET support; the ingester uses the GUID set/content hash and the 5-item feed limit. FCDO conditional GET can be added when ETag persistence is wired into `alert_sources`.
- Guest push token registration is schema-ready (`push_tokens.user_id` nullable, `installation_id`) but the public registration/fan-out path is left for the mobile slice.
- The production schema migration lives on the TanStack branch `feat/alerts-schema` (`0017_alerts.sql`); the backend test schema is `testdata/migrations/0013_alerts.sql`.

## Tests

```bash
go test ./internal/alerts ./internal/handler/expo ./internal/handler/admin -count=1
```

The integration tests use a PostGIS testcontainer and cover migration application, CAP ingest end-to-end, upsert/idempotency/material updates, fan-out preference matching, alert list/read/preferences, and admin Program Update create/publish/list.
