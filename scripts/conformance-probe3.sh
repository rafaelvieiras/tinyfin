#!/usr/bin/env bash
# Generates the container and probe fixtures of the conformance tree (library
# "Probe3", lazy; docs/contract/fixtures.json): transport streams in their
# framings, Matroska/WebM files told apart by their streams, files whose
# extension names another container, pixel formats with odd bit depths and a
# MLP track. Run by scripts/conformance-media.sh, or alone:
#
#   scripts/conformance-probe3.sh /path/to/media-root
#
# Needs ffmpeg (FFMPEG; jellyfin-ffmpeg works) with libx264, libx265, libvpx,
# libopus, ffv1 and python3.
set -euo pipefail
ROOT="${1:?usage: conformance-probe3.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"
P3="$ROOT/Probe3"
mkdir -p "$P3"

V="testsrc=size=160x120:rate=10:duration=3"
A="sine=frequency=440:duration=3:sample_rate=48000"
H264=(-c:v libx264 -preset ultrafast -pix_fmt yuv420p)
VP9=(-c:v libvpx-vp9 -deadline realtime -pix_fmt yuv420p)
ff() { "$FFMPEG" -v error -y "$@"; }
# The bit rates of the transport streams (playback9 "ts-bitrate") are pinned to
# the bytes the encoders write, so those fixtures are made single threaded and
# bit-exact (no encoder / muxer version strings, no time stamps of the run).
# The bytes still depend on the libx264 / aac encoder build: generate them with
# the reference's own jellyfin-ffmpeg (FFMPEG=...), as scripts/conformance-media.sh
# does, to get the same files on every machine.
# -cpuflags 0: the encoders' SIMD paths give slightly different bytes from one
# CPU to the next (an AAC track came out 582 bps heavier on the CI runner than
# on a desktop), and playback9's rates follow the bytes; the pure C paths are
# the same everywhere.
BX=(-threads 1 -cpuflags 0 -fflags +bitexact -flags +bitexact -map_metadata -1)
# The bytes depend on the encoder build, so a build that is not jellyfin-ffmpeg is
# replaced by the one inside a Jellyfin container image (docker; JF_IMAGE, default
# jellyfin/jellyfin:12.2) when docker is available; otherwise only a warning.
case "$("$FFMPEG" -version 2>/dev/null | head -1)" in
  *ellyfin*) ;;
  *)
    if [ -z "${JFFMPEG:-}" ] && command -v docker >/dev/null 2>&1; then
      r="$(realpath "$ROOT")"
      JFWRAP="$(mktemp)"
      printf '#!/bin/sh\nexec docker run --rm -i -u "$(id -u):$(id -g)" -v "%s:%s" -w "%s" --entrypoint /usr/lib/jellyfin-ffmpeg/ffmpeg "%s" "$@"\n' \
        "$r" "$r" "$r" "${JF_IMAGE:-jellyfin/jellyfin:12.2}" >"$JFWRAP"
      chmod +x "$JFWRAP"; trap 'rm -f "$JFWRAP"' EXIT
      echo "Probe3: $FFMPEG is not jellyfin-ffmpeg; using the Jellyfin container image" >&2
      FFMPEG="$JFWRAP"
    elif [ -n "${JFFMPEG:-}" ]; then FFMPEG="$JFFMPEG"
    else echo "Probe3: warning: $FFMPEG is not jellyfin-ffmpeg and docker is missing; the ts-bitrate figures of playback9 may differ" >&2
    fi ;;
esac
# <stem incl. id> <ext> <ffmpeg args...> : one movie
mk() {
  local d="$P3/$1"; local o="$d/$1.$2"; shift 2
  [ -f "$o" ] && return 0
  mkdir -p "$d"
  ff "$@" "$o" || { rm -f "$o"; echo "Probe3: $o failed" >&2; }
}
lav() { echo -f lavfi -i "$1"; }

# transport streams: 188-byte packets, and the 192-byte packets of M2TS whose 4-byte header is
# a time stamp (valid), all zero, or missing (a plain stream under the M2TS name)
mk "P3 Ts Aac (3801)" ts $(lav $V) $(lav $A) -ac 2 "${H264[@]}" -c:a aac -b:a 128k "${BX[@]}" -f mpegts
mk "P3 M2ts (3802)" m2ts $(lav $V) $(lav $A) -ac 2 "${H264[@]}" -c:a aac -b:a 128k "${BX[@]}" -f mpegts -mpegts_m2ts_mode 1
mk "P3 Mts (3806)" mts $(lav $V) $(lav $A) -ac 2 "${H264[@]}" -c:a ac3 -b:a 192k "${BX[@]}" -f mpegts
python3 - "$P3" <<'PY'
import os, shutil, sys
p3 = sys.argv[1]
def put(name, ext, data):
    d = f"{p3}/{name}"
    o = f"{d}/{name}.{ext}"
    if os.path.exists(o):
        return
    os.makedirs(d, exist_ok=True)
    open(o, "wb").write(data)
m2ts = open(f"{p3}/P3 M2ts (3802)/P3 M2ts (3802).m2ts", "rb").read()
plain = open(f"{p3}/P3 Ts Aac (3801)/P3 Ts Aac (3801).ts", "rb").read()
zero = bytearray(m2ts)
for i in range(0, len(zero), 192):
    zero[i:i + 4] = b"\0\0\0\0"
put("P3 M2ts Zero (3803)", "m2ts", bytes(zero))
put("P3 Ts Plain M2ts (3804)", "m2ts", plain)
put("P3 M2ts As Ts (3805)", "ts", m2ts)
PY

# Matroska: WebM only when every stream is a VP8/VP9/AV1 picture or a Vorbis/Opus sound
mk "P3 Webm Streams (3811)" mkv $(lav $V) $(lav $A) "${VP9[@]}" -c:a libopus -f matroska
mk "P3 Vp9 Aac (3812)" webm $(lav $V) $(lav $A) "${VP9[@]}" -c:a aac -f matroska
printf 'WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhi\n\n' >"$P3/.s.vtt"
mk "P3 Vp9 Subtitle (3813)" mkv $(lav $V) $(lav $A) -i "$P3/.s.vtt" -map 0 -map 1 -map 2 "${VP9[@]}" -c:a libopus -c:s webvtt -f matroska
rm -f "$P3/.s.vtt"

# extensions that name another container than the file holds
mk "P3 Mp4 (3820)" mp4 $(lav $V) $(lav $A) "${H264[@]}" -c:a aac -f mp4
mk "P3 Avi (3823)" avi $(lav $V) $(lav $A) "${H264[@]}" -c:a mp3 -f avi
mk "P3 Mpg (3824)" mpg $(lav $V) $(lav $A) -c:v mpeg2video -c:a mp2 -f mpeg
for spec in "3821:Mp4 As Mkv:3820:P3 Mp4 (3820).mp4:mkv" "3822:Mp4 As M4v:3820:P3 Mp4 (3820).mp4:m4v" "3825:Avi As Mp4:3823:P3 Avi (3823).avi:mp4"; do
  IFS=: read -r id label _ src ext <<<"$spec"
  d="$P3/P3 $label ($id)"; o="$d/P3 $label ($id).$ext"
  [ -f "$o" ] || { mkdir -p "$d"; cp "$P3/${src%.*}/$src" "$o"; }
done

# pixel formats: the bit depth follows the format, planar RGB of 8 bits has none
mk "P3 Gbrp9 (3831)" mkv $(lav $V) -vf format=gbrp9le -c:v ffv1 -f matroska
mk "P3 Yuv444p16 (3832)" mkv $(lav $V) -vf format=yuv444p16le -c:v ffv1 -f matroska
mk "P3 Vp9 Gbrp (3833)" mkv $(lav $V) -vf format=gbrp -c:v libvpx-vp9 -deadline realtime -f matroska

# a MLP track states no bit rate: the estimate is that of TrueHD
mk "P3 Audio Mlp (3841)" mkv $(lav $V) $(lav "anullsrc=channel_layout=stereo:sample_rate=48000:d=3") "${H264[@]}" -c:a mlp -strict -2 -fflags +bitexact -f matroska

# picture codecs against the transcoding profiles
mk "P3 H264 Mp4 (3851)" mp4 $(lav $V) $(lav $A) "${H264[@]}" -c:a aac -f mp4
mk "P3 Hevc Mp4 (3852)" mp4 $(lav $V) $(lav $A) -c:v libx265 -preset ultrafast -pix_fmt yuv420p -x265-params log-level=error -c:a aac -f mp4
echo "Probe3 ready"
