#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$DIR/scripts/lib/common.sh"

load_env "$DIR"

echo "Building server..."
go build -o "$DIR/bin/server" "$DIR/cmd/server"

echo "Starting server..."
exec "$DIR/bin/server"
