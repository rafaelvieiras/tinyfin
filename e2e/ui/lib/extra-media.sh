#!/usr/bin/env bash
# Extra fixtures the playback specs need that scripts/conformance-media.sh does not provide:
# a series whose first episode is long enough (>= 10 min) for the web client's "Up Next"
# popup, which it never shows for the 4-6 s conformance clips. Built once into a cache
# and copied next to the main media tree.
#   extra-media.sh <media-root>
set -euo pipefail
ROOT="${1:?usage: extra-media.sh <media-root>}"
CACHE="${UI_EXTRA_MEDIA_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/tinyfin-ui/media-extra}"
FFMPEG="${FFMPEG:-ffmpeg}"
clip() { # <out> <seconds> <tone-hz>
  [ -f "$1" ] && return 0
  mkdir -p "$(dirname "$1")"
  "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=$2:size=320x240:rate=1" -f lavfi -i "sine=frequency=$3:duration=$2" \
    -c:v libx264 -preset ultrafast -g 5 -pix_fmt yuv420p -c:a aac -shortest "$1"
}
S="$CACHE/UpNext/Up Show (2022)/Season 01"
clip "$S/Up Show (2022) S01E01.mp4" 700 330
clip "$S/Up Show (2022) S01E02.mp4" 8 392
mkdir -p "$ROOT"
cp -a "$CACHE/UpNext" "$ROOT/"
