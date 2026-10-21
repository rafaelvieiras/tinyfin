#!/usr/bin/env bash
# Pictures of other shapes for the encoded-HLS scenarios (library "Hls3", lazy):
# a 642x362 picture with a 4:3 sample aspect ratio (odd sizes after scaling),
# a 10-bit H.264 picture and an HEVC one, 8 s each at 24 fps with a mono tone.
#
#   scripts/conformance-hls3.sh /path/to/media-root
set -euo pipefail
ROOT="${1:?usage: conformance-hls3.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"
mk() { # <name> <size> <extra ffmpeg video args...>
  local name="$1" size="$2"; shift 2
  local out="$ROOT/Hls3/$name/$name.mp4"
  [ -f "$out" ] && return 0
  mkdir -p "$(dirname "$out")"
  "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=8:size=$size:rate=24" \
    -f lavfi -i "sine=frequency=330:duration=8" "$@" -c:a aac -shortest "$out" ||
    { rm -f "$out"; echo "Hls3: $name failed" >&2; }
}
mk "Odd Clip (2401)" 642x362 -vf setsar=4/3 -c:v libx264 -preset ultrafast -pix_fmt yuv420p
mk "Ten Bit (2402)" 640x360 -c:v libx264 -preset ultrafast -pix_fmt yuv420p10le -profile:v high10
mk "Hevc Clip (2403)" 640x360 -c:v libx265 -x265-params log-level=error -preset ultrafast -pix_fmt yuv420p -tag:v hvc1
# A 5.1 AC-3 sound (silent) under a small picture, for the audio re-encode rules.
six="$ROOT/Hls3/Six Chan (2404)/Six Chan (2404).mkv"
if [ ! -f "$six" ]; then
  mkdir -p "$(dirname "$six")"
  "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=8:size=320x180:rate=24" \
    -f lavfi -i "anullsrc=channel_layout=5.1:sample_rate=48000" -t 8 \
    -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a ac3 \
    -disposition:v:0 default -disposition:a:0 default -output_ts_offset 0.005 "$six" ||
    { rm -f "$six"; echo "Hls3: six failed" >&2; }
fi
