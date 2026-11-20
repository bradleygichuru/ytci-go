#!/usr/bin/env bash
# Render Cron Job entrypoint: dump Postgres and upload the dump + checksum to R2.
#
# Required environment:
#   DATABASE_URL          internal Render Postgres URL (not PgBouncer)
#   R2_ACCOUNT_ID         Cloudflare account id
#   R2_BACKUP_BUCKET      e.g. ytci-pg-backups
#   R2_BACKUP_ACCESS_KEY_ID
#   R2_BACKUP_SECRET_ACCESS_KEY
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${R2_ACCOUNT_ID:?R2_ACCOUNT_ID is required}"
: "${R2_BACKUP_BUCKET:?R2_BACKUP_BUCKET is required}"
: "${R2_BACKUP_ACCESS_KEY_ID:?R2_BACKUP_ACCESS_KEY_ID is required}"
: "${R2_BACKUP_SECRET_ACCESS_KEY:?R2_BACKUP_SECRET_ACCESS_KEY is required}"

export AWS_ACCESS_KEY_ID="$R2_BACKUP_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$R2_BACKUP_SECRET_ACCESS_KEY"
export AWS_DEFAULT_REGION="auto"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DAY="$(date -u +%Y/%m/%d)"
FILE="/tmp/ytci-$STAMP.dump"
ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"

pg_dump --dbname="$DATABASE_URL" --format=custom --no-owner --no-privileges --file="$FILE"
sha256sum "$FILE" > "$FILE.sha256"

aws s3 cp "$FILE" "s3://${R2_BACKUP_BUCKET}/daily/${DAY}/$(basename "$FILE")" \
  --endpoint-url "$ENDPOINT" --only-show-errors
aws s3 cp "$FILE.sha256" "s3://${R2_BACKUP_BUCKET}/daily/${DAY}/$(basename "$FILE").sha256" \
  --endpoint-url "$ENDPOINT" --only-show-errors

rm -f "$FILE" "$FILE.sha256"
echo "backup uploaded: daily/${DAY}/$(basename "$FILE")"
