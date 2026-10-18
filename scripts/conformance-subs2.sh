#!/usr/bin/env bash
# Generates the subtitle sidecar fixtures of the conformance tree (library
# "Subs2", lazy; docs/contract/fixtures.json): VobSub .idx/.sub pairs, Matroska
# subtitle files (.mks) and subtitle files whose content is not what their
# extension says. Run by scripts/conformance-media.sh, or alone:
#
#   scripts/conformance-subs2.sh /path/to/media-root
#
# Needs ffmpeg (FFMPEG, jellyfin-ffmpeg works), mkvextract and python3.
set -euo pipefail
ROOT="${1:?usage: conformance-subs2.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"
HERE="$(cd "$(dirname "$0")" && pwd)"
S2="$ROOT/Subs2"

# a 4 s 320x240 h264/aac clip
clip() { # <out>
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] || "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=4:size=320x240:rate=6" \
    -f lavfi -i "sine=frequency=440:duration=4" -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac -shortest "$1"
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# the picture track every VobSub and bitmap .mks fixture carries: the words
# BURN IN from 0.5 s to 3.5 s (a PGS stream, re-encoded as DVD subtitles)
python3 "$HERE/conformance-pgs.py" "$work/a.sup" 320 240 "BURN IN" 0.5 3.5
"$FFMPEG" -v error -y -f lavfi -i "color=c=black:s=320x240:r=6:d=4" -i "$work/a.sup" -map 0 -map 1 \
  -c:v libx264 -preset ultrafast -c:s dvdsub -t 4 "$work/dvd.mkv"
mkvextract tracks "$work/dvd.mkv" "1:$work/vob.idx" >/dev/null
[ -f "$work/vob.idx" ] && [ -f "$work/vob.sub" ]

# <dir name> <suffix between the video name and the extension, with its dot>
vobpair() {
  local d="$S2/$1/$1"
  clip "$d.mp4"
  [ -f "$d$2.idx" ] || cp "$work/vob.idx" "$d$2.idx"
  [ -f "$d$2.sub" ] || cp "$work/vob.sub" "$d$2.sub"
}
vobpair "Vob Pair (3900)" ""
vobpair "Vob Lang (3901)" ".en"
# a second pair of another language, flagged forced by its name
d="$S2/Vob Lang (3901)/Vob Lang (3901)"
[ -f "$d.fr.forced.idx" ] || sed 's/^id: en,/id: fr,/' "$work/vob.idx" >"$d.fr.forced.idx"
[ -f "$d.fr.forced.sub" ] || cp "$work/vob.sub" "$d.fr.forced.sub"

# an index with no data file, and a data file with no index
clip "$S2/Vob IdxOnly (3902)/Vob IdxOnly (3902).mp4"
[ -f "$S2/Vob IdxOnly (3902)/Vob IdxOnly (3902).idx" ] || cp "$work/vob.idx" "$S2/Vob IdxOnly (3902)/Vob IdxOnly (3902).idx"
clip "$S2/Vob SubOnly (3903)/Vob SubOnly (3903).mp4"
[ -f "$S2/Vob SubOnly (3903)/Vob SubOnly (3903).sub" ] || cp "$work/vob.sub" "$S2/Vob SubOnly (3903)/Vob SubOnly (3903).sub"

# one index with two tracks (en: two cues, fr: one) over one data file
vobpair "Vob Multi (3904)" ""
python3 - "$S2/Vob Multi (3904)/Vob Multi (3904).idx" <<'PY'
import re, sys
p = sys.argv[1]
t = open(p).read()
head = t.split("id: ")[0]
open(p, "w").write(head + "id: en, index: 0\ntimestamp: 00:00:00:523, filepos: 000000000\n"
                    "timestamp: 00:00:02:000, filepos: 000000800\n\n"
                    "id: fr, index: 1\ntimestamp: 00:00:01:000, filepos: 000001000\n")
PY

# Matroska subtitle files: a SubRip track flagged default (en), a PGS track
# (fr) and a text file that is not Matroska at all (de)
M="$S2/Mks Files (3905)/Mks Files (3905)"
clip "$M.mp4"
printf '1\n00:00:00,500 --> 00:00:03,500\nBURN IN TEXT\n' >"$work/a.srt"
[ -f "$M.en.mks" ] || "$FFMPEG" -v error -y -i "$work/a.srt" -map 0 -c copy -disposition:s:0 default -f matroska "$M.en.mks"
[ -f "$M.fr.mks" ] || "$FFMPEG" -v error -y -i "$work/a.sup" -map 0 -c copy -f matroska "$M.fr.mks"
[ -f "$M.de.mks" ] || printf '1\n00:00:01,000 --> 00:00:02,000\nHello\n' >"$M.de.mks"

# files whose content is not what the extension says, and empty or garbage ones
O="$S2/Sub Odd (3906)/Sub Odd (3906)"
clip "$O.mp4"
put() { [ -f "$O.$1" ] || printf '%b' "$2" >"$O.$1"; }
put en.vtt '1\n00:00:01,000 --> 00:00:02,000\nHello\n\n2\n00:00:03,000 --> 00:00:04,000\nWorld\n'
put fr.sub '{25}{50}Hello\n{75}{100}World|two\n'
put de.sub '[10][30]Hello\n[40][60]World\n'
put es.srt ''
put it.srt 'just some words\n'
put pt.ass 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n'
put ru.smi '<SAMI><BODY>\n<SYNC Start=1000><P Class=ENCC>Hello\n<SYNC Start=2000><P Class=ENCC>&nbsp;\n</BODY></SAMI>\n'
put ja.sub '[INFORMATION]\n[TITLE]x\n[END INFORMATION]\n[SUBTITLE]\n[COLF]&HFFFFFF,[STYLE]bd,[SIZE]18,[FONT]Arial\n00:00:01.00,00:00:02.00\nHello\n\n'
[ -f "$O.zh.sup" ] || cp "$work/a.srt" "$O.zh.sup"
