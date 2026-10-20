#!/usr/bin/env bash
# Generates the third batch of subtitle fixtures of the conformance tree
# (library "Subs3", lazy; docs/contract/fixtures.json): ASS files with fonts that
# are not installed, text files in other encodings (for the burn-in `charenc`),
# and external picture subtitles beside a video (VobSub, PGS, Matroska). Run by
# scripts/conformance-media.sh, or alone:
#
#   scripts/conformance-subs3.sh /path/to/media-root
#
# Needs ffmpeg (FFMPEG, jellyfin-ffmpeg works), mkvextract and python3.
set -euo pipefail
ROOT="${1:?usage: conformance-subs3.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"
HERE="$(cd "$(dirname "$0")" && pwd)"
S3="$ROOT/Subs3"

# a 4 s 320x240 h264/aac clip
clip() { # <out>
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] || "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=4:size=320x240:rate=6" \
    -f lavfi -i "sine=frequency=440:duration=4" -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac -shortest "$1"
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# ASS files whose style font is not installed (and one that is Arial)
ass() { # <dir name> <font>
  local d="$S3/$1/$1"
  clip "$d.mp4"
  [ -f "$d.en.ass" ] || cat >"$d.en.ass" <<ASS
[Script Info]
ScriptType: v4.00+
PlayResX: 320
PlayResY: 240

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,$2,28,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:03.50,Default,,0,0,0,,BURN IN TEXT
ASS
}
ass "Font Missing (4000)" "Zzyzx Nonexistent Font"
ass "Font Arial (4001)" "Arial"
ass "Font Serif (4002)" "Times New Roman"

# text files in other encodings: <dir name> <file suffix> <python expression of the bytes>
enc() {
  local d="$S3/$1/$1"
  clip "$d.mp4"
  [ -f "$d.$2" ] || python3 -c "import sys; sys.stdout.buffer.write($3)" >"$d.$2"
}
srt='"1\n00:00:00,500 --> 00:00:03,500\n%s\n"'
enc "Enc Latin1 (4010)" en.srt "($srt % 'Olá ação').encode('latin-1')"
enc "Enc Cp1252 (4011)" en.srt "($srt % '“Hello” – œ').encode('cp1252')"
enc "Enc Cp1251 (4012)" en.srt "($srt % 'Привет мир').encode('cp1251')"
enc "Enc Gbk (4013)" en.srt "($srt % '你好世界').encode('gbk')"
enc "Enc Sjis (4014)" en.srt "($srt % 'こんにちは').encode('shift_jis')"
enc "Enc Utf16 (4015)" en.srt "($srt % 'Hello').encode('utf-16')"
enc "Enc Utf16Nobom (4016)" en.srt "($srt % 'Hello').encode('utf-16-le')"
enc "Enc Utf8 (4017)" en.srt "($srt % 'Olá').encode('utf-8')"
enc "Enc Short (4018)" en.srt "b'1\n00:00:00,500 --> 00:00:03,500\n\xe9\n'"
