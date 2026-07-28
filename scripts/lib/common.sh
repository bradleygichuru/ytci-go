load_env() {
  local dir="$1"
  if [ -f "$dir/.env.local" ]; then
    set -a
    source "$dir/.env.local"
    set +a
  fi
}
