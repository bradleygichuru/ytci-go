#!/usr/bin/env bash
# Uploads the Big Five hero images to R2 and prints the storage keys.
# Requires R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_BUCKET.
# Images are Wikimedia Commons lead images; credits are recorded by big-five.sql.
set -euo pipefail
: "${R2_ACCOUNT_ID:?}" "${R2_ACCESS_KEY_ID:?}" "${R2_SECRET_ACCESS_KEY:?}" "${R2_BUCKET:?}"
BASE="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
UA='YTCI-internal-seed/1.0'
DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT

declare -A PAGES=(
 [lion]=Lion
 [african-elephant]=African_bush_elephant
 [african-buffalo]=African_buffalo
 [african-leopard]=African_leopard
 [black-rhinoceros]=Black_rhinoceros
)

for slug in "${!PAGES[@]}"; do
  page="${PAGES[$slug]}"
  meta=$(curl -sS -A "$UA" "https://en.wikipedia.org/api/rest_v1/page/summary/${page}")
  thumb=$(echo "$meta" | python3 -c "import json,sys;print(json.load(sys.stdin).get('thumbnail',{}).get('source',''))")
  orig=$(echo "$meta" | python3 -c "import json,sys;print(json.load(sys.stdin).get('originalimage',{}).get('source',''))")
  big=$(echo "$thumb" | sed -E 's#/[0-9]+px-#/1200px-#')
  curl -sSL -A "$UA" "$big" -o "$DIR/${slug}.jpg"
  size=$(wc -c < "$DIR/${slug}.jpg")
  if [ "$size" -lt 20000 ] && [ -n "$orig" ]; then
    curl -sSL -A "$UA" "$orig" -o "$DIR/${slug}.jpg"
    size=$(wc -c < "$DIR/${slug}.jpg")
  fi
  [ "$size" -gt 20000 ] || { echo "image too small for $slug ($size)"; exit 1; }
  curl -sS -o /dev/null -w "${slug} %{http_code}\n" -X PUT \
    --aws-sigv4 "aws:amz:auto:s3" --user "${R2_ACCESS_KEY_ID}:${R2_SECRET_ACCESS_KEY}" \
    -H 'Content-Type: image/jpeg' --data-binary "@$DIR/${slug}.jpg" \
    "$BASE/$R2_BUCKET/species/big-five/${slug}.jpg"
done
