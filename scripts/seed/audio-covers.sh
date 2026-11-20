#!/usr/bin/env bash
set -euo pipefail
ENV=/home/brad/code/render-env2.json
get() { python3 -c "import json,sys;d=json.load(open('$ENV'));print(next(x['envVar']['value'] for x in d if x['envVar']['key']==sys.argv[1]))" "$1"; }
ACC=$(get R2_ACCOUNT_ID); KEY=$(get R2_ACCESS_KEY_ID); SEC=$(get R2_SECRET_ACCESS_KEY); BUCKET=$(get R2_BUCKET)
BASE="https://${ACC}.r2.cloudflarestorage.com"
declare -A VIDEOS=(
 [african-heritage-house]="/home/brad/Downloads/African heritage (single).mp4"
 [nabongo-cultural-centre]="/home/brad/Downloads/Nabongo Mumia Cultural Centre.mp4"
 [nairobi-war-cemetery]="/home/brad/Downloads/Nairobi War Cemetery.mp4"
 [nairobi-railway-museum]="/home/brad/Downloads/NAIROBI RAILWAYS.mp4"
)
mkdir -p /home/brad/code/audio-covers
for slug in "${!VIDEOS[@]}"; do
  src="${VIDEOS[$slug]}"
  [ -f "$src" ] || { echo "missing video $src"; exit 1; }
  dur=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$src")
  seek=$(python3 -c "print(max(1.0, float('$dur')*0.3))")
  out="/home/brad/code/audio-covers/${slug}.jpg"
  ffmpeg -y -loglevel error -ss "$seek" -i "$src" -frames:v 1 -vf "scale=1200:-2" -q:v 3 "$out"
  size=$(wc -c < "$out")
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT \
    --aws-sigv4 "aws:amz:auto:s3" --user "${KEY}:${SEC}" \
    -H 'Content-Type: image/jpeg' --data-binary "@$out" \
    "$BASE/$BUCKET/audio-stories/covers/${slug}.jpg")
  echo "${slug} dur=${dur}s size=${size} -> $code"
done
