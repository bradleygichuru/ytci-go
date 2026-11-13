# Backup and restore runbook

Scope: `ytci-go` Postgres on Render, receipt objects in R2, and the logical
restore drill used to prove the backup path. Account-owner provisioning is
tracked in #212; this runbook records the procedure and the drill evidence.

## Recovery targets

| Tier | Mechanism | RPO | Retention | Restore granularity |
|---|---|---|---|---|
| Hot | Render PITR | ≤ 10 min | 3 days (Hobby) | Any second in window |
| Cold | Daily `pg_dump` to `ytci-pg-backups` R2 | ≤ 24 h | 35 days | Full database |
| Product | `trip_events` + `plan_revisions` | Per edit | Life of trip | Per trip/revision |

PITR is disaster recovery, not user undo. User-visible restore comes from the
audit/revision layer (`POST /v1/mobile/itineraries/{id}/revisions/{id}/restore`).

## Provisioning (owner-only, #212)

1. Confirm the Render Postgres instance is on a paid compute plan. Free
   databases have no backups and expire.
2. Keep the workspace on Hobby for the included 3-day PITR window unless 7 days
   is a hard requirement.
3. Create a Render Cron Job (03:00 UTC) that runs `scripts/backup-db.sh` and
   uploads the dump to the separate `ytci-pg-backups` R2 bucket under
   `daily/YYYY/MM/DD/`.
4. Apply a bucket lock to `daily/` for 35 days and use a write-only R2 token
   scoped to that bucket for the cron.
5. Add a failure notification and a weekly check that the newest dump is less
   than 26 hours old.
6. Record the workspace plan, database plan, region, PostgreSQL version,
   storage size, PITR window, and latest restore timestamp in the operations
   log.

## Logical backup

```bash
export DATABASE_URL='postgres://...'
scripts/backup-db.sh ./backups
```

The script writes a custom-format dump and a `.sha256` sidecar. Use the
**internal** Render database URL, not PgBouncer, for `pg_dump`.

## Restore drill

The drill creates a throwaway database, restores a fresh dump, runs sanity
checks, and reports RTO.

```bash
SOURCE_DATABASE_URL='postgres://brad@127.0.0.1:5432/ytci?sslmode=disable' \
DRILL_DATABASE_URL='postgres://brad@127.0.0.1:5432/ytci_restore_drill?sslmode=disable' \
scripts/restore-drill.sh
```

Last local drill (2026-11-13):

```text
restore drill: ok
itineraries: 8
trip_events: 35
postgis: 3.5 USE_GEOS=1 USE_PROJ=1 USE_STATS=1
rto_seconds: 2
```

The drill database is dropped at the end. Re-run it after schema migrations
and before release.

## Render PITR procedure

1. Record `T0` UTC, the reporter, the symptom, and the last known-good time.
   Do not delete or recreate the database.
2. Stop writes (maintenance mode or suspend the web service) before restoring.
3. Note the deployed commit at the target time. If migrations ran after it,
   plan to redeploy that commit or run forward migrations before cutover.
4. Render Dashboard → database → **Recovery** → **Point-in-Time Recovery** →
   **Restore Database**. Create `ytci-pg-restore-YYYYMMDD-HHMM`; keep the
   existing settings/IP allow list.
5. Wait for `Available`, then validate in isolation:
   ```sql
   select now();
   select postgis_version();
   select count(*) from itineraries;
   select count(*) from trip_events;
   select max(created_at) from trip_events;
   ```
   Spot-check a known trip, expense/settle-up total, and receipt `object_key`.
6. Repoint `DATABASE_URL` to the recovery instance and redeploy. Writes after
   the restore target are lost by design.

## Receipt objects

- Receipts are immutable per upload; a replacement creates a new key and row.
- Delete is soft for 30 days, then purge the R2 object. This is longer than the
  PITR window so a restore cannot point at a deleted object.
- Exports include receipt metadata only; receipt binaries are never included.
- Account deletion anonymizes shared-trip receipt ownership; personal-only
  objects follow the 30-day purge.

## Verification checklist

- [ ] Paid database and 3-day PITR window confirmed.
- [ ] Daily cron produces a dump < 26 hours old.
- [ ] `restore-drill.sh` passes after the latest migration.
- [ ] Restored counts for itineraries, members, expenses, shares, settlements,
      trip events, and plan revisions match the source.
- [ ] PostGIS extension is present.
- [ ] Receipt metadata references an existing object key (or is marked
      unavailable).
