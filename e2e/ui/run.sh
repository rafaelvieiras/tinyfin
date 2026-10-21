#!/usr/bin/env bash
# Browser-test battery launcher: reference Jellyfin 12.2 web UI vs the Tinyfin server, same web build.
#
#   TARGET=ref|tinyfin|both ./run.sh [--update-golden] [--keep] [-- <playwright args>]
#
# Default TARGET=both. After a run that included tinyfin the baseline ratchet (ratchet.mjs) is
# applied: a baselined spec that no longer passes fails the run; other failures are reported as
# known divergences. --update-golden requires TARGET=ref and rewrites golden/*.json.
# --keep leaves the servers running (prints the state dir; stop with: ./down.sh <dir>).
# Optional ~/.config/tinyfin-sala/credentials.env: a non-empty TINYFIN_TMDB_API_KEY is given to
# tinyfin only (values never printed). The reference stays offline (REF_DOCKER_ARGS default).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
export TARGET="${TARGET:-both}"
KEEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --update-golden)
      [ "$TARGET" = ref ] || { echo "--update-golden only runs against the reference: use TARGET=ref" >&2; exit 2; }
      export UPDATE_GOLDEN=1 ;;
    --keep) KEEP=1 ;;
    --) shift; break ;;
    *) break ;;
  esac
  shift
done
[ -d node_modules ] || npm install --no-audit --no-fund >/dev/null

STATE=""
cleanup() { [ -n "$STATE" ] && [ "$KEEP" = 0 ] && bash "$HERE/down.sh" "$STATE"; return 0; }
trap cleanup EXIT

# up.sh prints STATE=<dir> first; its own failures leave the state dir for cleanup below
exec 3< <(bash "$HERE/up.sh" 2>&1; echo "UP_EXIT=$?")
UPRC=1
while IFS= read -r line <&3; do
  case "$line" in
    STATE=*) STATE="${line#STATE=}" ;;
    UP_EXIT=*) UPRC="${line#UP_EXIT=}" ;;
    *) echo "$line" ;;
  esac
done
[ "$UPRC" = 0 ] || { echo "servers failed to start" >&2; exit 1; }
# shellcheck disable=SC1091
. "$STATE/env"
export REF_URL="${REF_URL:-}" TINYFIN_URL="${TF_URL:-}"
[ "$KEEP" = 1 ] && echo "kept running; state: $STATE (stop: $HERE/down.sh $STATE)"

rc=0
npx playwright test "$@" || rc=$?
if [ "$TARGET" != ref ] && [ -z "${UPDATE_GOLDEN:-}" ]; then
  node "$HERE/ratchet.mjs" results.json || rc=$?
fi
exit "$rc"
