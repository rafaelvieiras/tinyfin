#!/usr/bin/env bash
# Pictures deeper than 8 bits at known rates for the rate ladder scenarios
# (library "Rate10", lazy): 10-bit H.264 at 80 kb/s .. 12 Mb/s, one 10-bit
# HEVC and an 8-bit control, 3 s each, no sound (a file without sound adds none
# to the announced bandwidth). The encodes are single-threaded and bit-exact so
# the same ffmpeg build writes the same bytes; the scenarios assert the figures
# the reference announced for the files of its own jellyfin-ffmpeg: another
# build writes other sizes. So the encodes use JFFMPEG=<binary>, else the binary
# of a Jellyfin container image (docker; JF_IMAGE, default jellyfin/jellyfin:12.2).
# Without either the library is skipped and its scenarios are not runnable.
#
#   scripts/conformance-rate10.sh /path/to/media-root
set -euo pipefail
ROOT="${1:?usage: conformance-rate10.sh <media-root>}"
mkdir -p "$ROOT"
if [ -n "${JFFMPEG:-}" ]; then FFMPEG=("$JFFMPEG")
elif command -v docker >/dev/null 2>&1; then
  r="$(realpath "$ROOT")"
  FFMPEG=(docker run --rm -u "$(id -u):$(id -g)" -v "$r:$r" -w "$r" --entrypoint /usr/lib/jellyfin-ffmpeg/ffmpeg "${JF_IMAGE:-jellyfin/jellyfin:12.2}")
  ROOT="$r"
else echo "Rate10: skipped (no JFFMPEG and no docker)"; exit 0; fi
mk() { # <name> <h264|hevc> <pix_fmt> <kbit/s>
  local name="$1" codec="$2" pix="$3" k="$4"
  local out="$ROOT/Rate10/$name/$name.mkv"
  [ -f "$out" ] && return 0
  mkdir -p "$(dirname "$out")"
  local enc
  if [ "$codec" = h264 ]; then
    enc=(-c:v libx264 -preset ultrafast -b:v "${k}k" -minrate "${k}k" -maxrate "${k}k" -bufsize "${k}k"
      -x264-params nal-hrd=cbr:force-cfr=1)
  else
    enc=(-c:v libx265 -preset ultrafast -b:v "${k}k"
      -x265-params "vbv-maxrate=${k}:vbv-bufsize=${k}:strict-cbr=1:frame-threads=1:pools=none:log-level=error")
  fi
  "${FFMPEG[@]}" -v error -y -f lavfi -i "testsrc2=duration=3:size=640x360:rate=24" \
    -vf "noise=alls=20:allf=t,format=$pix" "${enc[@]}" -threads 1 -an \
    -fflags +bitexact -flags:v +bitexact "$out" ||
    { rm -f "$out"; echo "Rate10: $name failed" >&2; }
}
mk "Ten 80k (2801)" h264 yuv420p10le 80
mk "Ten 800k (2802)" h264 yuv420p10le 800
mk "Ten 1500k (2803)" h264 yuv420p10le 1500
mk "Ten 3000k (2804)" h264 yuv420p10le 3000
mk "Ten 6000k (2805)" h264 yuv420p10le 6000
mk "Ten 8000k (2806)" h264 yuv420p10le 8000
mk "Ten 12000k (2807)" h264 yuv420p10le 12000
mk "Ten Hevc 1000k (2808)" hevc yuv420p10le 1000
mk "Eight 800k (2809)" h264 yuv420p 800
echo "Rate10 ready"
