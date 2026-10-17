#!/usr/bin/env bash
# Serves the media the .strm fixture "Strm Http (2345)" points at, on all
# interfaces of the host so that both Tinyfin (host) and the reference
# (Docker container, through the bridge gateway) reach it.
#
#   scripts/conformance-strm-serve.sh <media-root> [port]   # prints pid=...; kill that pid to stop
#
# Run scripts/conformance-media.sh first (it writes <media-root>/.strm-http/).
set -euo pipefail
ROOT="${1:?usage: conformance-strm-serve.sh <media-root> [port]}"
PORT="${2:-18999}"
cd "$ROOT/.strm-http"
nohup python3 -m http.server "$PORT" --bind 0.0.0.0 >"$ROOT/.strm-http.log" 2>&1 &
echo $! >"$ROOT/.strm-http.pid"
for _ in $(seq 1 20); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PORT}/strm-src.mp4")" = 200 ] && break
  sleep 0.5
done
echo "strm media: http://127.0.0.1:${PORT}/strm-src.mp4 pid=$(cat "$ROOT/.strm-http.pid")"
