#!/usr/bin/env bash
# Starts a fresh, throwaway Tinyfin for conformance runs on 127.0.0.1:<port>
# with an admin seeded directly in the database (the seed also marks the
# first-run wizard as completed; the wizard itself is covered by the
# fresh-server scenarios, see docs/contract/README.md).
#
#   scripts/conformance-tinyfin-up.sh [port]   # prints pid + connection details
#
# BIND (default 127.0.0.1) is the address to listen on. Scenarios that need the
# server to see the runner at a non-loopback address (users.auth.R21) need
# BIND=<one of the host's own addresses> and the runner pointed at it.
# The data directory (the one that holds the PIN files) is printed as
# data-dir=...; pass it to the runner as -server-data-dir.
set -euo pipefail
PORT="${1:-18098}"
BIND="${BIND:-127.0.0.1}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Conformance-Admin-1}"
DIR="$(mktemp -d /tmp/tinyfin-conformance.XXXXXX)"
cd "$(dirname "$0")/.."
go build -o "$DIR/tinyfin" ./cmd/tinyfin
go build -o "$DIR/conformance" ./cmd/conformance
"$DIR/conformance" seed-admin -db "$DIR/tinyfin.db" -user "$ADMIN_USER" -pass "$ADMIN_PASS"
nohup "$DIR/tinyfin" -data "$DIR/tinyfin.db" -addr "${BIND}:${PORT}" >"$DIR/server.log" 2>&1 &
echo $! >"$DIR/pid"
for _ in $(seq 1 30); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://${BIND}:${PORT}/System/Info/Public")" = 200 ] && break
  sleep 1
done
echo "tinyfin ready: http://${BIND}:${PORT} admin=$ADMIN_USER pass=$ADMIN_PASS dir=$DIR data-dir=$DIR pid=$(cat "$DIR/pid")"
