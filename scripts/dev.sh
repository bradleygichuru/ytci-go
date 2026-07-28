#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$DIR/scripts/lib/common.sh"

export PATH="$PATH:$(go env GOPATH)/bin"

if ! command -v air &>/dev/null; then
  echo "Installing air (live-reload) ..."
  go install github.com/air-verse/air@latest
fi

load_env "$DIR"

exec air -c "$DIR/.air.toml"
