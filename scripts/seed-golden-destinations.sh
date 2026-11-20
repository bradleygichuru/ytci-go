#!/usr/bin/env bash
# Publish/refresh the 10 golden destination fixtures.
#
# Usage:
#   DATABASE_URL=postgres://... scripts/seed-golden-destinations.sh
#
# Apply migrations first. The seed is idempotent and archives uncurated
# aliases/duplicates. Unknown fees/hours stay marked not_verified/unverified.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f "$ROOT/testdata/seeds/golden_destinations.sql"
