#!/usr/bin/env bash
# Generates the fourth batch of subtitle fixtures of the conformance tree
# (library "Subs4", lazy; docs/contract/fixtures.json): an ASS file whose font is
# not installed with text in scripts no Latin font covers (burn-in fallback
# fonts), and Matroska subtitle files whose content is plain text. Run by
# scripts/conformance-media.sh, or alone:
#
#   scripts/conformance-subs4.sh /path/to/media-root
#
# Needs ffmpeg (FFMPEG, jellyfin-ffmpeg works).
set -euo pipefail
ROOT="${1:?usage: conformance-subs4.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"
S4="$ROOT/Subs4"

# a 4 s 320x240 h264/aac clip
clip() { # <out>
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] || "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=4:size=320x240:rate=6" \
    -f lavfi -i "sine=frequency=440:duration=4" -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac -shortest "$1"
}

# an ASS file whose style font is not installed, with Latin, CJK, Cyrillic and Greek text
d="$S4/Font Cjk (4101)/Font Cjk (4101)"
clip "$d.mp4"
[ -f "$d.en.ass" ] || cat >"$d.en.ass" <<'ASS'
[Script Info]
ScriptType: v4.00+
PlayResX: 320
PlayResY: 240

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Zzyzx Nonexistent Font,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:03.50,Default,,0,0,0,,Missing font Olá ação
Dialogue: 0,0:00:00.00,0:00:03.50,Default,,0,0,60,,你好世界 こんにちは 안녕
Dialogue: 0,0:00:00.00,0:00:03.50,Default,,0,0,120,,Привет мир Γεια
ASS

# Matroska subtitle files whose content is plain text (not Matroska): SubRip,
# WebVTT and ASS, by language
d="$S4/Mks Plain (4100)/Mks Plain (4100)"
clip "$d.mp4"
[ -f "$d.de.mks" ] || printf '1\n00:00:01,000 --> 00:00:02,000\nHello\n\n2\n00:00:03,000 --> 00:00:03,500\nWorld\n' >"$d.de.mks"
[ -f "$d.en.mks" ] || printf 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n' >"$d.en.mks"
[ -f "$d.fr.mks" ] || printf '[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Default,Arial,20\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n' >"$d.fr.mks"
