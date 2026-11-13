# Render backup cron

Build and push this image to a registry Render can pull from, then create a
Render Cron Job:

- Schedule: `0 3 * * *` UTC
- Command: image entrypoint (no command needed)
- Environment group: `ytci-backup`
  - `DATABASE_URL` — internal Render Postgres URL, not PgBouncer
  - `R2_ACCOUNT_ID`
  - `R2_BACKUP_BUCKET=ytci-pg-backups`
  - `R2_BACKUP_ACCESS_KEY_ID`
  - `R2_BACKUP_SECRET_ACCESS_KEY`

Use an R2 token scoped to the backup bucket only. Put a bucket lock on the
`daily/` prefix for 35 days. Record the RPO as ≤24h and alert if the newest
object is older than 26h.

Local test:

```bash
docker build -t ytci-backup-cron scripts/backup-cron
docker run --rm --env-file .env.backup ytci-backup-cron
```
