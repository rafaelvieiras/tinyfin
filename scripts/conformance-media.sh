#!/usr/bin/env bash
# Generates the tiny synthetic media tree the conformance fixtures
# (docs/contract/fixtures.json) point at. Deterministic file names, ~4-6 s clips (plus one 330 s clip),
# h264/aac mp4 so every item is Direct-Play-compatible with a browser profile.
#
#   scripts/conformance-media.sh /path/to/media-root
set -euo pipefail
ROOT="${1:?usage: conformance-media.sh <media-root>}"
FFMPEG="${FFMPEG:-ffmpeg}"

clip() { # <out> <seconds> <tone-hz> [fps] [WxH]
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] && return 0
  "$FFMPEG" -v error -y -f lavfi -i "testsrc=duration=$2:size=${5:-320x240}:rate=${4:-24}" \
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

# Sort-name and premiere-date movies (own library "Sorting", lazy): leading
# articles, non-ASCII names, digit runs, punctuation and premiere dates that
# disagree with the folder year.
sorting() { # <folder title> <year> [nfo title] [premiered] [sort title]
  local dir="$ROOT/Sorting/$1 ($2)" file="$ROOT/Sorting/$1 ($2)/$1 ($2).mp4"
  clip "$file" 1 210 4
  # Always carry a title in an .nfo so the item name is explicit (the
  # servers differ in whether a folder's "(Year)" stays in the name).
  {
    printf '<movie><title>%s</title><year>%s</year>' "${3:-$1}" "$2"
    [ -n "${4:-}" ] && printf '<premiered>%s</premiered>' "$4"
    [ -n "${5:-}" ] && printf '<sorttitle>%s</sorttitle>' "$5"
    printf '</movie>\n'
  } >"$dir/$1 ($2).nfo"
}
sorting "The Zeta Sort" 2012
sorting "A Yankee Sort" 2013
sorting "An Xray Sort" 2014
sorting "Éclair Sort" 2015
sorting "Ångström Sort" 2016
sorting "Zulu Sort" 2017
sorting "Apple Sort" 2011
sorting "Theater Sort" 2018
sorting "Number Ten Sort" 2021 "Number 10 Sort"
sorting "Number Two Sort" 2022 "Number 2 Sort"
sorting "Punct Sort" 2023 "Mr. Hyphen-Ated's Sort"
sorting "Sort Title Sort" 2024 "Zebra Sort" "" "An Aardvark Sort"
sorting "Premiere Early" 2020 "" "2001-03-04"
sorting "Premiere Late" 2005 "" "2019-07-08"
sorting "Premiere Mid" 2012 "" "2012-06-15"

# Transliteration movies (own library "Translit", lazy): titles in non-Latin
# scripts, carried by an .nfo so every server reads the same name; the folder
# name is ASCII. Used by items.sort.transliteration.
translit() { # <folder> <year> <title>
  local dir="$ROOT/Translit/$1 ($2)"
  clip "$dir/$1 ($2).mp4" 1 $((600 + $2 % 100)) 4
  printf '<movie><title>%s</title><year>%s</year></movie>\n' "$3" "$2" >"$dir/$1 ($2).nfo"
}
translit "Tr Cyrillic" 2101 "Привет"
translit "Tr Cyrillic Punct" 2102 "Привет, мир! (1)"
translit "Tr Slavic" 2103 "Ђорђе Їжак Щастие"
translit "Tr Greek" 2104 "Άγγελος Ευρώ"
translit "Tr Hebrew" 2105 "שלום"
translit "Tr Arabic" 2106 "مرحبا ٣"
translit "Tr Devanagari" 2107 "नमस्ते"
translit "Tr Thai" 2108 "สวัสดี"
translit "Tr Kana" 2109 "ガギグ テスト"
translit "Tr Hangul" 2110 "한국어 제목"
translit "Tr Han" 2111 "東京 2020 中文"
translit "Tr Han Mixed" 2112 "Tokyo東京"
translit "Tr Latin Punct" 2113 "Café! (2)"
translit "Tr Sharp S" 2114 "Straße!"
translit "Tr Mixed" 2115 "Éclair Привет!"

# A series whose names exercise the sort-name rules (own library "SortShows", lazy): leading
# article on the series and on an episode title, episodes in two seasons.
SS="$ROOT/SortShows/The Sort Show (2024)"
clip "$SS/Season 01/The Sort Show (2024) S01E01.mp4" 1 310 4
clip "$SS/Season 01/The Sort Show (2024) S01E02.mp4" 1 320 4
clip "$SS/Season 02/The Sort Show (2024) S02E01.mp4" 1 330 4
printf '<tvshow><title>The Sort Show</title></tvshow>\n' >"$SS/tvshow.nfo"
printf '<episodedetails><title>The Opening</title><season>1</season><episode>1</episode></episodedetails>\n' >"$SS/Season 01/The Sort Show (2024) S01E01.nfo"
printf '<episodedetails><title>Second Chapter</title><season>1</season><episode>2</episode></episodedetails>\n' >"$SS/Season 01/The Sort Show (2024) S01E02.nfo"
printf '<episodedetails><title>An Opener</title><season>2</season><episode>1</episode></episodedetails>\n' >"$SS/Season 02/The Sort Show (2024) S02E01.nfo"

# Larger frames (own library "Sizes", lazy): the dimension filters (HD / 4K buckets).
clip "$ROOT/Sizes/HD Size (2030)/HD Size (2030).mp4" 1 240 2 1280x720
clip "$ROOT/Sizes/UHD Size (2031)/UHD Size (2031).mp4" 1 250 1 3840x2160
# bucket edges: widths 1200 / 1198 and 3800, height 2100
clip "$ROOT/Sizes/Edge W1200 (2032)/Edge W1200 (2032).mp4" 1 260 1 1200x100
clip "$ROOT/Sizes/Edge W1198 (2033)/Edge W1198 (2033).mp4" 1 270 1 1198x100
clip "$ROOT/Sizes/Edge W3800 (2034)/Edge W3800 (2034).mp4" 1 280 1 3800x100
clip "$ROOT/Sizes/Edge H2100 (2035)/Edge H2100 (2035).mp4" 1 290 1 100x2100
clip "$ROOT/Sizes/Edge W1300H2200 (2036)/Edge W1300H2200 (2036).mp4" 1 300 1 1300x2200

# 150 tiny movies (own library "Many", lazy): the default page size check.
many_src="$ROOT/.src/many.mp4"
clip "$many_src" 1 200 4
for i in $(seq 0 149); do
  n=$(printf 'Bulk %03d' "$i")
  d="$ROOT/Many/$n ($((2000 + i % 20)))"
  mkdir -p "$d"
  [ -f "$d/$n ($((2000 + i % 20))).mp4" ] || cp "$many_src" "$d/$n ($((2000 + i % 20))).mp4"
done
# Names with surrounding white space, series runtime, episodes without an
# SxxExx pattern and a home-videos tree (own libraries "Trim", "TrimShows",
# "Loose", "Home", lazy). Files are copies of one tiny clip; the .nfo files
# carry a UTF-8 byte order mark as Jellyfin writes them.
BOM=$'\xef\xbb\xbf'
NBSP=$' '
IDEO=$'　'
ZWSP=$'​'
vid() { mkdir -p "$(dirname "$1")"; [ -f "$1" ] || cp "$many_src" "$1"; }
nfo() { mkdir -p "$(dirname "$1")"; printf '%s%s\n' "$BOM" "$2" >"$1"; }

# Movies whose .nfo titles carry white space (and a few that are not
# white space), plus names taken from file names.
T="$ROOT/Trim"
vid "$T/Trim Space (2101)/Trim Space (2101).mp4"
nfo "$T/Trim Space (2101)/Trim Space (2101).nfo" "<movie><title>  Trim Lead and Trail  </title><sorttitle>  The Trim Sort  </sorttitle><originaltitle> Trim Original </originaltitle><plot>
 Trim plot line 1
line 2 
</plot></movie>"
vid "$T/Trim Tab (2102)/Trim Tab (2102).mp4"
nfo "$T/Trim Tab (2102)/Trim Tab (2102).nfo" "<movie><title>	Trim Tab	</title></movie>"
vid "$T/Trim Space Variants (2103)/Trim Space Variants (2103).mp4"
nfo "$T/Trim Space Variants (2103)/Trim Space Variants (2103).nfo" "<movie><title>${NBSP}${IDEO} Trim Variants ${IDEO}${NBSP}</title></movie>"
vid "$T/Trim Inner (2104)/Trim Inner (2104).mp4"
nfo "$T/Trim Inner (2104)/Trim Inner (2104).nfo" "<movie><title>Trim   Inner  Run</title></movie>"
vid "$T/Trim Zwsp (2105)/Trim Zwsp (2105).mp4"
nfo "$T/Trim Zwsp (2105)/Trim Zwsp (2105).nfo" "<movie><title>${ZWSP}Trim Zwsp${ZWSP}</title></movie>"
vid "$T/Trim Blank/Trim Blank.mp4"
nfo "$T/Trim Blank/Trim Blank.nfo" "<movie><title>   </title><plot>  </plot></movie>"
vid "$T/ Trim Lead File/ Trim Lead File .mp4"

# Series: .nfo titles, named season, series runtime, a folder name with white
# space, episodes named by file or by .nfo.
TS="$ROOT/TrimShows"
S="$TS/Trim Show (2110)"
nfo "$S/tvshow.nfo" "<tvshow><title>  Trim Show Title  </title><sorttitle> The Trim Show Sort </sorttitle><originaltitle> Trim Show Original </originaltitle><plot> Trim show plot </plot><runtime>0</runtime><namedseason number=\"1\"> Trim Named Season </namedseason></tvshow>"
vid "$S/Season 01/Trim Show S01E01.mp4"
nfo "$S/Season 01/Trim Show S01E01.nfo" "<episodedetails><title> Trim Ep Title </title><sorttitle> The Trim Ep Sort </sorttitle><originaltitle> Trim Ep Original </originaltitle><season>1</season><episode>1</episode><plot /></episodedetails>"
vid "$S/Season 01/Trim Show S01E02 - File Title  .mp4"
vid "$S/Season 01/Trim Show S01E03.mp4"
nfo "$S/Season 01/Trim Show S01E03.nfo" "<episodedetails><title>	Trim Tab Ep
</title><plot>
 Trim ep plot 
</plot></episodedetails>"
vid "$TS/ Trim Lead Show  (2111) /Season 1 /ep S01E01.mp4"
S="$TS/Run Twenty (2112)"
nfo "$S/tvshow.nfo" "<tvshow><title>Run Twenty</title><runtime>20</runtime></tvshow>"
vid "$S/Season 01/Run Twenty S01E01.mp4"
S="$TS/Run None (2113)"
nfo "$S/tvshow.nfo" "<tvshow><title>Run None</title></tvshow>"
vid "$S/Season 01/Run None S01E01.mp4"
S="$TS/Run Text (2114)"
nfo "$S/tvshow.nfo" "<tvshow><title>Run Text</title><runtime>20 min</runtime></tvshow>"
vid "$S/Season 01/Run Text S01E01.mp4"
S="$TS/Run Negative (2115)"
nfo "$S/tvshow.nfo" "<tvshow><title>Run Negative</title><runtime>-5</runtime></tvshow>"
vid "$S/Season 01/Run Negative S01E01.mp4"
S="$TS/Run Fraction (2116)"
nfo "$S/tvshow.nfo" "<tvshow><title>Run Fraction</title><runtime>12.5</runtime></tvshow>"
vid "$S/Season 01/Run Fraction S01E01.mp4"

# Episodes without an SxxExx pattern: specials, loose files in season folders
# and files directly in the series folder.
L="$ROOT/Loose/Loose Show (2120)"
epnfo() { # <path without extension> <inner xml>
  nfo "$1.nfo" "<episodedetails>$2</episodedetails>"
}
vid "$L/Season 00/Short One.mp4"
epnfo "$L/Season 00/Short One" "<title>Short One</title><season>0</season><plot />"
vid "$L/Season 00/Holiday Special - Story Name.mp4"
epnfo "$L/Season 00/Holiday Special - Story Name" "<title>Holiday Special</title><season>0</season>"
vid "$L/Season 00/S00E01 - Promo.mp4"
vid "$L/Season 00/Zed NoNfo.mp4"
vid "$L/Season 00/Alpha WithEp.mp4"
epnfo "$L/Season 00/Alpha WithEp" "<title>Alpha With Ep</title><season>0</season><episode>5</episode>"
vid "$L/Season 01/Loose Show S01E01.mp4"
vid "$L/Season 01/Loose In Season.mp4"
vid "$L/Season 01/Loose Nfo.mp4"
epnfo "$L/Season 01/Loose Nfo" "<title>Loose Nfo Title</title><season>1</season>"
vid "$L/Season 01/Pattern Nfo S01E07.mp4"
epnfo "$L/Season 01/Pattern Nfo S01E07" "<title>Pattern Nfo</title><episode>8</episode>"
vid "$L/Root Loose.mp4"
vid "$L/Root Nfo.mp4"
epnfo "$L/Root Nfo" "<title>Root Nfo Title</title><season>2</season><episode>4</episode>"
vid "$L/Root Nfo NoSeason.mp4"
epnfo "$L/Root Nfo NoSeason" "<title>Root Nfo NoSeason Title</title><episode>4</episode>"
vid "$L/Loose Show S01E09.mp4"
vid "$ROOT/Loose/Loose Only (2121)/Only One.mp4"
vid "$ROOT/Loose/Loose Specials (2122)/Specials/Sp One.mp4"
epnfo "$ROOT/Loose/Loose Specials (2122)/Specials/Sp One" "<title>Sp One Title</title><season>0</season>"
vid "$ROOT/Loose/Loose Specials (2122)/Season 01/Loose Specials S01E01.mp4"

# Home videos: nested folders, no naming scheme.
H="$ROOT/Home"
vid "$H/TV/Channel One/Bumper/Channel One - Bumper - 2002 - Game.mp4"
vid "$H/TV/Channel Two/Nick Clip.mp4"
vid "$H/TV/Channel Two/Nfo Video.mp4"
nfo "$H/TV/Channel Two/Nfo Video.nfo" "<movie><title> Nfo Video Title </title><plot>Nfo plot</plot><year>1999</year><premiered>1999-02-03</premiered></movie>"
vid "$H/TV/Channel Two/Movie Nfo Video.mp4"
nfo "$H/TV/Channel Two/movie.nfo" "<movie><title>Folder Wide Title</title></movie>"
vid "$H/TV/Channel Two/Sub/Deep/Deep Clip.mp4"
vid "$H/Root Video.mp4"
vid "$H/Dotted.Name.2002.1080p.mp4"
vid "$H/Mixed Case/aaa lower.mp4"
vid "$H/Mixed Case/Bbb Upper.mp4"
vid "$H/Mixed Case/10 Ten.mp4"
vid "$H/Mixed Case/2 Two.mp4"
vid "$H/Mixed Case/The Article.mp4"
mkdir -p "$H/Empty Folder"
vid "$H/ Lead Space Folder /  lead file .mp4"
# left out of the catalog
printf 'notes' >"$H/TV/Channel Two/notes.txt"
vid "$H/.hidden/Secret.mp4"
vid "$H/#recycle/Gone.mp4"
vid "$H/Root Video-trailer.mp4"

echo "media tree ready under $ROOT"
