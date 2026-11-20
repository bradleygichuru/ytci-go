#!/usr/bin/env bash
# Restore a logical dump into a target database.
#
# Usage:
#   TARGET_DATABASE_URL=postgres://... scripts/restore-db.sh <dump-file>
#
# This is destructive to the target database.
set -euo pipefail

: "${TARGET_DATABASE_URL:?TARGET_DATABASE_URL is required}"
DUMP="${1:?usage: restore-db.sh <dump-file>}"

pg_restore \
  --dbname="$TARGET_DATABASE_URL" \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges \
  "$DUMP"
