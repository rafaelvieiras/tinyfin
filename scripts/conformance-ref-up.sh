#!/usr/bin/env bash
# Starts a throwaway reference server (Jellyfin 12.x) for validating the
# conformance scenarios, completes its first-run wizard through the public API
# and prints the connection details. Black-box use only: the container is an
# HTTP endpoint, nothing from it is read as source.
#
#   scripts/conformance-ref-up.sh <media-root> [port]      # start
#   docker rm -f "${REF_NAME:-tinyfin-conformance-ref}"     # stop (REF_NAME isolates parallel runs)
set -euo pipefail
MEDIA="${1:?usage: conformance-ref-up.sh <media-root> [port]}"
PORT="${2:-18096}"
IMAGE="${REF_IMAGE:-jellyfin/jellyfin:12.2}"
NAME="${REF_NAME:-tinyfin-conformance-ref}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Conformance-Admin-1}"

docker rm -f "$NAME" >/dev/null 2>&1 || true
# The media tree is mounted writable: the subtitle upload scenarios have the
# server save files beside the media (and delete them again).
# REF_DOCKER_ARGS: extra `docker run` flags, e.g. "--dns 127.0.0.1" to keep the
# server offline so online metadata providers cannot rename the fixtures.
# shellcheck disable=SC2086
docker run -d --name "$NAME" --memory 1g -p "127.0.0.1:${PORT}:8096" ${REF_DOCKER_ARGS:-} \
  --tmpfs /config:uid=1000 --tmpfs /cache:uid=1000 \
  -v "$(realpath "$MEDIA")":/media "$IMAGE" >/dev/null

base="http://127.0.0.1:${PORT}"
for _ in $(seq 1 60); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$base/System/Info/Public")" = 200 ] && break
  sleep 2
done
j() { curl -fsS -m 20 -H 'Content-Type: application/json' "$@"; }
# the public info endpoint answers before the startup wizard is ready
for _ in $(seq 1 60); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$base/Startup/Configuration")" = 200 ] && break
  sleep 2
done
j -X POST "$base/Startup/Configuration" -d '{"UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"}' >/dev/null
j "$base/Startup/User" >/dev/null
j -X POST "$base/Startup/User" -d "{\"Name\":\"$ADMIN_USER\",\"Password\":\"$ADMIN_PASS\"}" >/dev/null
j -X POST "$base/Startup/Complete" >/dev/null
echo "reference ready: $base  admin=$ADMIN_USER pass=$ADMIN_PASS media-root(in server)=/media"
