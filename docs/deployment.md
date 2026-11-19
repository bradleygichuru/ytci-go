# Deployment

The API is a single Go binary (`./cmd/server`). Production runs it on Render as
a **native Go** web service; `Dockerfile` is a portable build artifact for local
or container-based runs and is not the Render deploy path.

## Render service

| Setting | Value |
|---|---|
| Service | `ytci-go` (`srv-d9n9ffm417fc73cu3260`) |
| Branch | `master` |
| Auto-deploy | on every commit to `master` |
| Health check | `/health` |
| Build | `go build -o server ./cmd/server` |
| Start | `./server` |

Because auto-deploy is on, **merging any commit to `master` is a production
deploy**. Land code through a PR only when its CI is green and a deploy is
intended.

## Probes

| Path | Purpose | Auth | Behaviour |
|---|---|---|---|
| `GET /health` | Render health check (existing) | none | `200 {"status":"ok"}` while the process runs. Does not touch the database. |
| `GET /healthz` | Liveness alias | none | Same as `/health`. |
| `GET /readyz` | Readiness | none | `200` only when the database answers and `platform_contract_versions` contains the complete `1.0.0` contract set (migration 0069). Otherwise `503` with per-check detail. |

`/readyz` is deliberately not wired into the Render service yet: switching the
health check path is a service configuration change and should be paired with
applying the contract migrations to the target database.

## Required environment variables

Set these in the Render dashboard (or the equivalent staging service). No
values are committed.

Required at startup:

- `DATABASE_URL`
- `ADMIN_JWKS_URL`
- `GOOGLE_PLACES_API_KEY`
- `MICE_TICKET_SECRET` — required whenever `APP_ENV` is not `development`.

Deployment-specific:

- `APP_ENV` (`production` or `staging`)
- `CORS_ALLOWED_ORIGINS`
- `JWT_EXPECTED_ISSUER`
- `JWT_EXPECTED_AUDIENCE` (default `ytci-api`)

Optional:

- `EXPO_PUSH_TOKEN` — push workers stay idle without it.
- `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET` —
  media uploads/presigning.
- Then & Now and analytics flags; keep them `false` until their gates are
  signed.

`BETTER_AUTH_SECRET` is not read by this binary. It validates Better Auth
session tokens by looking up the shared `sessions` table in `DATABASE_URL`; the
TanStack admin/auth service owns the signing secret.

## Database migrations

There is no in-process migration runner. Apply `testdata/migrations/*.sql` in
filename order to the target database before considering it ready. `/readyz`
stays `503` until the contract schema is present.

## Deploy

With the Render CLI authenticated against the owning workspace:

```bash
render deploys create srv-d9n9ffm417fc73cu3260 --wait --confirm
```

The Render API key, workspace membership, and database URL are not stored in
this repository, so a deploy cannot be performed from CI or an agent without
them.

## Local container

```bash
docker build -t ytci-go .
docker run --rm -p 8080:8080 --env-file .env.local ytci-go
curl -s localhost:8080/healthz
curl -s localhost:8080/readyz
```

The runtime image includes `ca-certificates` (Expo, JWKS, R2) and `tzdata`
(quiet hours resolve `Africa/Nairobi` and other IANA zones).
