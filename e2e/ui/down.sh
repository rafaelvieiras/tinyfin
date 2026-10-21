#!/usr/bin/env bash
# Stops only what up.sh started for <state-dir> (container by name, tinyfin by PID).
# The tinyfin data dir (/tmp/tinyfin-conformance.*) is left in place by design.
set -uo pipefail
STATE="${1:?usage: down.sh <state-dir>}"
[ -f "$STATE/env" ] || exit 0
# shellcheck disable=SC1091
. "$STATE/env"
[ -n "${REF_CONTAINER:-}" ] && docker rm -f "$REF_CONTAINER" >/dev/null 2>&1
[ -n "${TF_PID:-}" ] && kill "$TF_PID" 2>/dev/null
rm -rf "$STATE"
exit 0
