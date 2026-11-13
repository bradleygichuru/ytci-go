#!/usr/bin/env bash
# Rehearse a full logical restore into a throwaway database and record RTO.
#
# Usage:
#   SOURCE_DATABASE_URL=postgres://.../ytci \
#   DRILL_DATABASE_URL=postgres://.../ytci_restore_drill \
#   scripts/restore-drill.sh [dump-file]
#
# If no dump is supplied, a fresh dump of SOURCE_DATABASE_URL is created first.
# The drill database is dropped and recreated, so never point it at production.
set -euo pipefail

: "${SOURCE_DATABASE_URL:?SOURCE_DATABASE_URL is required}"
: "${DRILL_DATABASE_URL:?DRILL_DATABASE_URL is required}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DUMP="${1:-}"
START="$(date +%s)"

if [ -z "$DUMP" ]; then
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT
  DUMP="$(
    DATABASE_URL="$SOURCE_DATABASE_URL" "$ROOT/scripts/backup-db.sh" "$TMP_DIR" | tail -n1
  )"
fi

DRILL_DB="$(printf '%s' "$DRILL_DATABASE_URL" | sed -E 's#^.*/([^/?]+)(\?.*)?$#\1#')"
ADMIN_URL="$(printf '%s' "$DRILL_DATABASE_URL" | sed -E 's#/[^/?]+(\?.*)?$#/postgres\1#')"

psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$DRILL_DB\" WITH (FORCE)" >/dev/null
psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$DRILL_DB\"" >/dev/null
TARGET_DATABASE_URL="$DRILL_DATABASE_URL" "$ROOT/scripts/restore-db.sh" "$DUMP"

ITINERARIES="$(psql "$DRILL_DATABASE_URL" -tAc 'SELECT count(*) FROM itineraries')"
EVENTS="$(psql "$DRILL_DATABASE_URL" -tAc 'SELECT count(*) FROM trip_events')"
POSTGIS="$(psql "$DRILL_DATABASE_URL" -tAc 'SELECT postgis_version()')"
SECONDS_ELAPSED="$(( $(date +%s) - START ))"

printf 'restore drill: ok\n'
printf 'dump: %s\n' "$DUMP"
printf 'itineraries: %s\n' "$ITINERARIES"
printf 'trip_events: %s\n' "$EVENTS"
printf 'postgis: %s\n' "$POSTGIS"
printf 'rto_seconds: %s\n' "$SECONDS_ELAPSED"

psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$DRILL_DB\" WITH (FORCE)" >/dev/null
