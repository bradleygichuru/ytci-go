#!/usr/bin/env bash
# Create a compressed logical backup and checksum.
#
# Usage:
#   DATABASE_URL=postgres://... scripts/backup-db.sh [output-dir]
#
# The dump is internally consistent but is not PITR; use Render PITR for
# minute-level recovery and this for the 35-day offsite retention tier.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
OUT="${1:-backups}"
mkdir -p "$OUT"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
FILE="$OUT/ytci-$STAMP.dump"

pg_dump \
  --dbname="$DATABASE_URL" \
  --format=custom \
  --no-owner \
  --no-privileges \
  --file="$FILE"

sha256sum "$FILE" > "$FILE.sha256"
printf '%s\n' "$FILE"
