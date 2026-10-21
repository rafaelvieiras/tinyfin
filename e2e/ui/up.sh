#!/usr/bin/env bash
# Starts what the UI battery needs and records it in a state dir:
#   - media tree (cached build, copied per server)
#   - the jellyfin-web build extracted from the reference image (.webdist)
#   - TARGET=ref|tinyfin|both -> reference container and/or tinyfin, with libraries
# Prints "STATE=<dir>"; stop with: down.sh <dir>. Stops only what this state dir started.
#
# Optional env file named by UI_CREDENTIALS_FILE is sourced if
# present; a non-empty TINYFIN_TMDB_API_KEY is passed to tinyfin only. Values are never printed.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
TARGET="${TARGET:-both}"
CRED="${UI_CREDENTIALS_FILE:-}"
if [ -f "$CRED" ]; then set -a; . "$CRED"; set +a; fi
[ -n "${TINYFIN_TMDB_API_KEY:-}" ] || unset TINYFIN_TMDB_API_KEY
export ADMIN_USER="${UI_ADMIN_USER:-admin}" ADMIN_PASS="${UI_ADMIN_PASS:-Conformance-Admin-1}"
export UI_ADMIN_USER="$ADMIN_USER" UI_ADMIN_PASS="$ADMIN_PASS"
IMAGE="${REF_IMAGE:-jellyfin/jellyfin:12.2}"
STATE="$(mktemp -d /tmp/tinyfin-ui.XXXXXX)"
echo "STATE=$STATE"
: >"$STATE/env"

freeport() { # first free TCP port at or above $1
  local p=$1
  while ss -ltn "sport = :$p" 2>/dev/null | grep -q LISTEN; do p=$((p + 1)); done
  echo "$p"
}

# 1. same web build on both sides
if [ ! -f "$HERE/.webdist/index.html" ]; then
  c="tinyfin-ui-webx-$$"
  docker create --name "$c" "$IMAGE" >/dev/null
  mkdir -p "$HERE/.webdist"
  docker cp "$c:/jellyfin/jellyfin-web/." "$HERE/.webdist/"
  docker rm -f "$c" >/dev/null
fi

# 2. media: build once into the cache, copy per server
CACHE="${UI_MEDIA_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/tinyfin-ui/media}"
bash "$REPO/scripts/conformance-media.sh" "$CACHE" >/dev/null 2>"$STATE/media.log" || { tail -5 "$STATE/media.log" >&2; exit 1; }

start_ref() {
  cp -a "$CACHE" "$STATE/media-ref"
  bash "$HERE/lib/extra-media.sh" "$STATE/media-ref"
  local port name; port="$(freeport "${REF_PORT:-18300}")"; name="tinyfin-ui-ref-$$"
  # offline DNS keeps online metadata providers from renaming the fixtures
  REF_NAME="$name" REF_IMAGE="$IMAGE" REF_DOCKER_ARGS="${REF_DOCKER_ARGS:---dns 127.0.0.1}" \
    bash "$REPO/scripts/conformance-ref-up.sh" "$STATE/media-ref" "$port" >/dev/null
  echo "REF_CONTAINER=$name" >>"$STATE/env"
  echo "REF_URL=http://127.0.0.1:$port" >>"$STATE/env"
  node "$HERE/lib/setup-libs.mjs" "http://127.0.0.1:$port" /media
}
start_tf() {
  cp -a "$CACHE" "$STATE/media-tf"
  bash "$HERE/lib/extra-media.sh" "$STATE/media-tf"
  local port out; port="$(freeport "${TF_PORT:-18400}")"
  out="$(TINYFIN_WEB_DIR="$HERE/.webdist" bash "$REPO/scripts/conformance-tinyfin-up.sh" "$port")"
  # the script prints credentials; keep only pid and dir
  echo "TF_PID=$(sed -n 's/.* pid=\([0-9]*\).*/\1/p' <<<"$out")" >>"$STATE/env"
  echo "TF_DIR=$(sed -n 's/.* dir=\([^ ]*\).*/\1/p' <<<"$out")" >>"$STATE/env"
  echo "TF_URL=http://127.0.0.1:$port" >>"$STATE/env"
  node "$HERE/lib/setup-libs.mjs" "http://127.0.0.1:$port" "$STATE/media-tf"
}
case "$TARGET" in
  ref) start_ref ;;
  tinyfin) start_tf ;;
  both) start_ref; start_tf ;;
  *) echo "TARGET must be ref|tinyfin|both" >&2; exit 2 ;;
esac
