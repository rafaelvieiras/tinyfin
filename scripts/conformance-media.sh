#!/usr/bin/env bash
# Generates the tiny synthetic media tree the conformance fixtures
# (docs/contract/fixtures.json) point at. Deterministic file names, ~4-6 s clips (plus one 330 s clip),
# h264/aac mp4 so every item is Direct-Play-compatible with a browser profile.
#
#   scripts/conformance-media.sh /path/to/media-root
set -euo pipefail
ROOT="${1:?usage: conformance-media.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"

clip() { # <out> <seconds> <tone-hz> [fps]
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] && return 0
  "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=$2:size=320x240:rate=${4:-24}" \
    -f lavfi -i "sine=frequency=$3:duration=$2" -c:v libx264 -preset ultrafast -pix_fmt yuv420p \
    -c:a aac -shortest "$1"
}

clip "$ROOT/Movies/Alpha Movie (2015)/Alpha Movie (2015).mp4"   6 440
clip "$ROOT/Movies/Bravo Movie (2020)/Bravo Movie (2020).mp4"   6 550
clip "$ROOT/Movies/Charlie Movie (2018)/Charlie Movie (2018).mp4" 6 660
# long enough (>= 300 s) to exercise the resume-position save branch
clip "$ROOT/Long/Long Movie (2021)/Long Movie (2021).mp4" 330 770 2
clip "$ROOT/Shows/Test Show (2019)/Season 01/Test Show (2019) S01E01.mp4" 4 330
clip "$ROOT/Shows/Test Show (2019)/Season 01/Test Show (2019) S01E02.mp4" 4 392
clip "$ROOT/Shows/Test Show (2019)/Season 02/Test Show (2019) S02E01.mp4" 4 494
# Rated movies (own library "Rated", created only around the scenarios that
# need it): one tiny clip each plus a Kodi-style .nfo carrying the rating.
rated() { # <title> <year> <mpaa or empty>
  local dir="$ROOT/Rated/$1 ($2)" file="$ROOT/Rated/$1 ($2)/$1 ($2).mp4"
  clip "$file" 2 220 6
  if [ -n "${3:-}" ]; then
    printf '<movie><title>%s</title><year>%s</year><mpaa>%s</mpaa></movie>\n' "$1" "$2" "$3" >"$dir/$1 ($2).nfo"
  fi
}
rated "Rated G" 2001 "G"
rated "Rated PG" 2002 "PG"
rated "Rated PG13" 2003 "PG-13"
rated "Rated R" 2004 "R"
rated "Rated NC17" 2005 "NC-17"
rated "Rated TVMA" 2006 "TV-MA"
rated "Rated TV14" 2007 "TV-14"
rated "Rated Twelve" 2008 "12"
rated "Rated Unknown" 2009 "ZZ-99"
rated "Rated None" 2010 ""
# A rated series (own library "RatedShows", lazy): the show carries TV-MA, the
# first episode no rating of its own, the second one TV-Y.
RS="$ROOT/RatedShows/Rated Show (2020)"
clip "$RS/Season 01/Rated Show (2020) S01E01.mp4" 2 330 6
clip "$RS/Season 01/Rated Show (2020) S01E02.mp4" 2 392 6
printf '<tvshow><title>Rated Show</title><mpaa>TV-MA</mpaa></tvshow>\n' >"$RS/tvshow.nfo"
printf '<episodedetails><title>Rated Episode Two</title><season>1</season><episode>2</episode><mpaa>TV-Y</mpaa></episodedetails>\n' >"$RS/Season 01/Rated Show (2020) S01E02.nfo"

# 150 tiny movies (own library "Many", lazy): the default page size check.
many_src="$ROOT/.src/many.mp4"
clip "$many_src" 1 200 4
for i in $(seq 0 149); do
  n=$(printf 'Bulk %03d' "$i")
  d="$ROOT/Many/$n ($((2000 + i % 20)))"
  mkdir -p "$d"
  [ -f "$d/$n ($((2000 + i % 20))).mp4" ] || cp "$many_src" "$d/$n ($((2000 + i % 20))).mp4"
done
echo "media tree ready under $ROOT"
