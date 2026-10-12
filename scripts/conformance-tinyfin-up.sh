#!/usr/bin/env bash
# Starts a fresh, throwaway Tinyfin for conformance runs on 127.0.0.1:<port>
# with an admin seeded directly in the database (no first-run wizard exists).
#
#   scripts/conformance-tinyfin-up.sh [port]   # prints pid + connection details
set -euo pipefail
PORT="${1:-18098}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Conformance-Admin-1}"
DIR="$(mktemp -d /tmp/tinyfin-conformance.XXXXXX)"
cd "$(dirname "$0")/.."
go build -o "$DIR/tinyfin" ./cmd/tinyfin
go build -o "$DIR/conformance" ./cmd/conformance
"$DIR/conformance" seed-admin -db "$DIR/tinyfin.db" -user "$ADMIN_USER" -pass "$ADMIN_PASS"
nohup "$DIR/tinyfin" -data "$DIR/tinyfin.db" -addr "127.0.0.1:${PORT}" >"$DIR/server.log" 2>&1 &
echo $! >"$DIR/pid"
for _ in $(seq 1 30); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/System/Info/Public")" = 200 ] && break
  sleep 1
done
echo "tinyfin ready: http://127.0.0.1:${PORT} admin=$ADMIN_USER pass=$ADMIN_PASS dir=$DIR pid=$(cat "$DIR/pid")"
