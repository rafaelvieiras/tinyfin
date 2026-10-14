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


# Reference naming without metadata files: movie names and years, episode
# numbering, season folders, several documents in one .nfo, photos and sort
# names (own libraries "Names", "EpNum", "SeasonDirs", "NfoMulti", "Photos",
# "SortWords", lazy). Files are copies of one tiny clip.
img() { # <out>: a tiny picture, format by extension
  mkdir -p "$(dirname "$1")"
  [ -f "$1" ] && return 0
  "$FFMPEG" -v error -y -f lavfi -i "color=c=red:s=40x30" -frames:v 1 "$1"
}
vid "$ROOT/Names/Foo One (2015)/Foo One (2015).mp4"
vid "$ROOT/Names/Foo Two (2016)/Other Name.mp4"
vid "$ROOT/Names/Foo Three.2017.1080p.BluRay/Foo Three.2017.1080p.BluRay.mp4"
vid "$ROOT/Names/Foo Twelve (1899)/Foo Twelve (1899).mp4"
vid "$ROOT/Names/Far (2100)/Far (2100).mp4"
vid "$ROOT/Names/Two Years 1999 2005/Two Years 1999 2005.mp4"
vid "$ROOT/Names/(2013) Paren Start/(2013) Paren Start.mp4"
vid "$ROOT/Names/Foo Seven (2021).mp4"
vid "$ROOT/Names/Foo.Eight.2022.mp4"
vid "$ROOT/Names/Loose H 1080p.mp4"
vid "$ROOT/Names/Loose Q 2009 1080p 2010.mp4"
vid "$ROOT/Names/Loose F (2005) [1080p].mp4"
vid "$ROOT/Names/Loose A (2001).mp4"
vid "$ROOT/Names/2012 (2009).mp4"
vid "$ROOT/Names/Loose Z - 2014 -.mp4"
vid "$ROOT/Names/Loose N2001.mp4"
vid "$ROOT/Names/Name [Group].mp4"
vid "$ROOT/Names/Brk [Group] Name.mp4"
vid "$ROOT/Names/Multi (2010)/part1.mp4"
vid "$ROOT/Names/Multi (2010)/part2.mp4"
vid "$ROOT/Names/Stacked (2011)/Stacked (2011) - part1.mp4"
vid "$ROOT/Names/Stacked (2011)/Stacked (2011) - part2.mp4"
vid "$ROOT/Names/Versions (2014)/Versions (2014).mp4"
vid "$ROOT/Names/Versions (2014)/Versions (2014) - 4K.mp4"
vid "$ROOT/Names/Collection/Inner One (2012)/Inner One (2012).mp4"
vid "$ROOT/Names/Collection/Inner Two (2013)/Inner Two (2013).mp4"
vid "$ROOT/EpNum/Series 00/Bat - 05.mp4"
vid "$ROOT/EpNum/Series 01/Bat 209.mp4"
vid "$ROOT/EpNum/Series 02/Bat 2020-01-31.mp4"
vid "$ROOT/EpNum/Series 03/Bat 31-01-2020.mp4"
vid "$ROOT/EpNum/Series 04/Bat 01-31-2020.mp4"
vid "$ROOT/EpNum/Series 05/Bat Season 2 Episode 13.mp4"
vid "$ROOT/EpNum/Series 06/Bat [S02E15].mp4"
vid "$ROOT/EpNum/Series 07/Bat 2020.mp4"
vid "$ROOT/EpNum/Series 08/Bat 3x09.mp4"
vid "$ROOT/EpNum/Series 09/Bat S01E05-E06.mp4"
vid "$ROOT/EpNum/Series 10/Bat S01E02E03E04.mp4"
vid "$ROOT/EpNum/Series 11/Bat S01E02.E03.mp4"
vid "$ROOT/EpNum/Series 12/Bat [01].mp4"
vid "$ROOT/EpNum/Series 13/Bat 12 Title.mp4"
vid "$ROOT/EpNum/Series 14/Bat 1080p.mp4"
vid "$ROOT/EpNum/Series 15/Bat Ep. 5.mp4"
vid "$ROOT/EpNum/Series 16/Bat 05 - 06.mp4"
vid "$ROOT/EpNum/Series 17/Bat S01E02 2020.mp4"
vid "$ROOT/EpNum/Series 18/Bat - 1080p.mp4"
vid "$ROOT/EpNum/Series 19/Bat E05.mp4"
vid "$ROOT/EpNum/Series 20/Bat Part 2.mp4"
vid "$ROOT/EpNum/Series 21/Bat Special.mp4"
vid "$ROOT/EpNum/Series 22/Yr Show (2019) S01E01.mp4"
vid "$ROOT/EpNum/Series 23/Yr.Show.S01E04.720p.WEB-DL.x264.mp4"
vid "$ROOT/EpNum/Season Series/Season 01/Bat 05.mp4"
vid "$ROOT/EpNum/Season Series/Season 01/Bat Special.mp4"
vid "$ROOT/EpNum/Season Series/Season 01/Bat 2020-01-31.mp4"
vid "$ROOT/EpNum/Season Series/Season 01/Bat S02E15.mp4"
vid "$ROOT/EpNum/Season Series/Season 01/Bat 209.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 01/Dirs Show S01E01.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 01/Disc A/Dirs Show S01E02.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 1 Bonus/Bonus S01E05.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Part 2/Dirs Show S02E01.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 03/Dirs Show S05E01.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 7 Redux/Dirs Show S07E02.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Extras/Extra One.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Behind The Scenes/BTS One.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Season 01/Subs/Skipped.mp4"
vid "$ROOT/SeasonDirs/Dirs Show (2003)/Loose Root.mp4"
vid "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E01E02.mp4"
nfo "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E01E02.nfo" '<episodedetails><title>Alpha</title><plot>PlotA</plot><season>1</season><episode>1</episode></episodedetails>
<episodedetails><title>Beta</title><plot>PlotB</plot><season>1</season><episode>3</episode></episodedetails>'
vid "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E04.mp4"
nfo "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E04.nfo" '<episodedetails><title>Rev One</title><season>1</season><episode>5</episode></episodedetails>
<episodedetails><title>Rev Two</title><season>1</season><episode>4</episode></episodedetails>'
vid "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E06.mp4"
nfo "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E06.nfo" '<episodedetails><title>A</title><season>1</season><episode>6</episode></episodedetails>
<episodedetails><title>B</title><season>1</season><episode>7</episode></episodedetails>
<episodedetails><title>C</title><season>1</season><episode>8</episode></episodedetails>'
vid "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E09.mp4"
nfo "$ROOT/NfoMulti/Multi Show (2004)/Season 01/Multi S01E09.nfo" '<episodedetails><title>Only Title</title><season>1</season><episode>9</episode></episodedetails>
<episodedetails><season>1</season><episode>10</episode></episodedetails>'
img "$ROOT/Photos/Album One/pic1.jpg"
vid "$ROOT/Photos/Album One/Clip.mp4"
img "$ROOT/Photos/Album One/Clip.jpg"
img "$ROOT/Photos/Album One/poster.png"
img "$ROOT/Photos/Album One/banner.jpg"
img "$ROOT/Photos/Pics Only/a.jpg"
img "$ROOT/Photos/Pics Only/b.png"
vid "$ROOT/Photos/Videos Only/Vid.mp4"
img "$ROOT/Photos/root.jpg"
vid "$ROOT/SortWords/so-return/so-return.mp4"
nfo "$ROOT/SortWords/so-return/so-return.nfo" '<movie><title>Return of the King</title></movie>'
vid "$ROOT/SortWords/so-rings/so-rings.mp4"
nfo "$ROOT/SortWords/so-rings/so-rings.nfo" '<movie><title>Lord of the Rings</title></movie>'
vid "$ROOT/SortWords/so-tale/so-tale.mp4"
nfo "$ROOT/SortWords/so-tale/so-tale.nfo" '<movie><title>A Tale of an Apple</title></movie>'
vid "$ROOT/SortWords/so-odyssey/so-odyssey.mp4"
nfo "$ROOT/SortWords/so-odyssey/so-odyssey.nfo" '<movie><title>2001 A Space Odyssey</title></movie>'
vid "$ROOT/SortWords/so-the3/so-the3.mp4"
nfo "$ROOT/SortWords/so-the3/so-the3.nfo" '<movie><title>The The The</title></movie>'
vid "$ROOT/SortWords/so-trail/so-trail.mp4"
nfo "$ROOT/SortWords/so-trail/so-trail.nfo" '<movie><title>Out of The</title></movie>'
vid "$ROOT/SortWords/so-dash/so-dash.mp4"
nfo "$ROOT/SortWords/so-dash/so-dash.nfo" '<movie><title>Hello - A World</title></movie>'
vid "$ROOT/SortWords/so-dots/so-dots.mp4"
nfo "$ROOT/SortWords/so-dots/so-dots.nfo" '<movie><title>Hello.The.World</title></movie>'
vid "$ROOT/SortWords/so-quote/so-quote.mp4"
nfo "$ROOT/SortWords/so-quote/so-quote.nfo" '<movie><title>Hello '\''The'\'' World</title></movie>'
vid "$ROOT/SortWords/so-amp/so-amp.mp4"
nfo "$ROOT/SortWords/so-amp/so-amp.nfo" '<movie><title>Rock &amp; The Roll</title></movie>'
vid "$ROOT/SortWords/so-many/so-many.mp4"
nfo "$ROOT/SortWords/so-many/so-many.nfo" '<movie><title>One An Two A Three The Four</title></movie>'
vid "$ROOT/SortWords/so-thea/so-thea.mp4"
nfo "$ROOT/SortWords/so-thea/so-thea.nfo" '<movie><title>Thea The</title></movie>'

# Artwork, extras and the residual shape rules (own libraries "Art",
# "ArtShows", "Extras", "ExtraShows", "Resid", lazy): picture names and
# extensions, trailers, special features, theme media, stacked parts,
# versions, merged season folders. Pictures are copies of one tiny image
# (the file name decides what they are), audio files copies of one tone.
pic_src="$ROOT/.src/pic.jpg"
if [ ! -f "$pic_src" ]; then
  mkdir -p "$ROOT/.src"
  "$FFMPEG" -v error -y -f lavfi -i "color=c=blue:s=200x300" -frames:v 1 "$pic_src"
fi
aud_src="$ROOT/.src/tone.mp3"
if [ ! -f "$aud_src" ]; then
  "$FFMPEG" -v error -y -f lavfi -i "sine=frequency=300:duration=1" -c:a libmp3lame "$aud_src"
fi
pic() { mkdir -p "$(dirname "$1")"; [ -f "$1" ] || cp "$pic_src" "$1"; }
aud() { mkdir -p "$(dirname "$1")"; [ -f "$1" ] || cp "$aud_src" "$1"; }

A="$ROOT/Art"
# Primary names: "<video>-poster" beats "poster", which beats "folder"; the
# extension order is png, jpg, jpeg, webp, tbn, gif.
M="$A/Art Primary (2201)"
vid "$M/Art Primary (2201).mp4"
pic "$M/poster.jpg"; pic "$M/poster.png"; pic "$M/folder.jpg"; pic "$M/cover.jpg"
pic "$M/Art Primary (2201)-poster.webp"
# Every other single picture kind, and names that no kind owns.
M="$A/Art Kinds (2202)"
vid "$M/Art Kinds (2202).mp4"
pic "$M/clearlogo.png"; pic "$M/clearart.jpg"; pic "$M/banner.jpg"; pic "$M/landscape.jpg"; pic "$M/thumb.jpg"
pic "$M/discart.jpg"; pic "$M/cdart.jpg"
pic "$M/box.jpg"; pic "$M/screenshot.jpg"; pic "$M/menu.jpg"; pic "$M/back.jpg"
# Backdrops: fanart, numbered forms, background, art, extrafanart/ and
# backdrop<N>; not fanart1, backdrop-1 or backdrop0.
M="$A/Art Backdrops (2203)"
vid "$M/Art Backdrops (2203).mp4"
pic "$M/fanart.jpg"; pic "$M/fanart-1.jpg"; pic "$M/fanart-2.jpg"; pic "$M/backdrop1.jpg"
pic "$M/art.jpg"; pic "$M/background.jpg"; pic "$M/extrafanart/a.jpg"; pic "$M/extrafanart/b.jpg"
pic "$M/fanart1.jpg"; pic "$M/backdrop-1.jpg"; pic "$M/backdrop0.jpg"
# A single "<video>-fanart" is listed at two backdrop indexes.
M="$A/Art Fanart Dup (2204)"
vid "$M/Art Fanart Dup (2204).mp4"
pic "$M/Art Fanart Dup (2204)-fanart.jpg"
# Recognized extensions and spellings (any case), and ones that are not.
M="$A/Art Ext Ok (2205)"
vid "$M/Art Ext Ok (2205).mp4"
pic "$M/POSTER.TBN"; pic "$M/Fanart.webp"; pic "$M/logo.jpeg"; pic "$M/Banner.GIF"
M="$A/Art Ext No (2206)"
vid "$M/Art Ext No (2206).mp4"
pic "$M/poster.avif"; pic "$M/fanart.bmp"; pic "$M/logo.heic"; pic "$M/banner.jpe"
# Several movies in one folder: only their own "<video>-<kind>" pictures are
# theirs; the folder's own artwork goes to the folder item.
M="$A/Art Multi"
vid "$M/Multi One (2207).mp4"; vid "$M/Multi Two (2207).mp4"
pic "$M/poster.jpg"; pic "$M/Multi One (2207)-poster.jpg"; pic "$M/Multi One (2207)-landscape.jpg"
pic "$M/Multi Two (2207)-fanart.jpg"; pic "$M/fanart.jpg"
# A movie that is not alone in its folder because it also has a sub folder.
M="$A/Art Sub (2208)"
vid "$M/Art Sub (2208).mp4"
pic "$M/poster.jpg"; pic "$M/logo.png"
mkdir -p "$M/Unrelated"; pic "$M/Unrelated/x.jpg"

AS="$ROOT/ArtShows"
S="$AS/Art Show (2210)"
vid "$S/Season 01/Art Show S01E01.mp4"
vid "$S/Season 01/Art Show S01E02.mp4"
vid "$S/Season 01/Art Show S01E03.mp4"
vid "$S/Season 02/Art Show S02E01.mp4"
vid "$S/Specials/Art Show S00E01.mp4"
pic "$S/poster.jpg"; pic "$S/folder.png"; pic "$S/banner.jpg"; pic "$S/clearart.jpg"; pic "$S/landscape.jpg"
pic "$S/fanart.jpg"; pic "$S/fanart-1.jpg"; pic "$S/extrafanart/a.jpg"
pic "$S/season01-poster.jpg"; pic "$S/season1-poster.png"; pic "$S/season01-banner.jpg"; pic "$S/season01-fanart.jpg"
pic "$S/season01-thumb.jpg"; pic "$S/season01.jpg"
pic "$S/season-specials-poster.jpg"
pic "$S/Season 01/folder.jpg"; pic "$S/Season 01/landscape.jpg"
pic "$S/Season 02/folder.jpg"; pic "$S/Season 02/logo.png"
pic "$S/Season 01/Art Show S01E01-thumb.jpg"
pic "$S/Season 01/Art Show S01E02.jpg"
pic "$S/Season 01/metadata/Art Show S01E03.jpg"
pic "$S/Season 02/Art Show S02E01-poster.jpg"

E="$ROOT/Extras"
M="$E/Ex All (2220)"
vid "$M/Ex All (2220).mp4"
vid "$M/Ex All (2220)-trailer.mp4"
vid "$M/trailers/Folder Trailer.mp4"
vid "$M/extras/Making Of.mp4"
vid "$M/featurettes/Deep Dive.mp4"
vid "$M/behind the scenes/On Set.mp4"
vid "$M/deleted scenes/Cut One.mp4"
vid "$M/interviews/Chat.mp4"
vid "$M/scenes/Opening.mp4"
vid "$M/samples/Taste.mp4"
vid "$M/shorts/Mini.mp4"
vid "$M/clips/Snip.mp4"
vid "$M/Ex All (2220)-featurette.mp4"
vid "$M/other/Misc.mp4"
vid "$M/extras/sub/Deep.mp4"
aud "$M/theme.mp3"; aud "$M/theme-music/Second Theme.mp3"; vid "$M/theme-music/not-audio.mp4"
vid "$M/backdrops/Loop.mp4"
aud "$M/soundtrack.mp3"; aud "$M/Ex All (2220) OST.mp3"
M="$E/Ex Names (2221)"
vid "$M/Ex Names (2221).mp4"
vid "$M/Ex Names (2221)-trailer.mp4"; vid "$M/trailer.mp4"; vid "$M/x-trailer.mp4"
vid "$M/Ex Names (2221)-behindthescenes.mp4"; vid "$M/Ex Names (2221)-deleted.mp4"
vid "$M/Ex Names (2221)-other.mp4"; vid "$M/Ex Names (2221)-sample.mp4"
M="$E/Ex Unowned"
vid "$M/Ex Unowned A (2222).mp4"; vid "$M/Ex Unowned B (2222).mp4"
vid "$M/Ex Unowned A (2222)-trailer.mp4"; vid "$M/trailers/Shared.mp4"; vid "$M/extras/Shared Extra.mp4"; aud "$M/theme.mp3"
M="$E/Ex Hidden (2223)"
vid "$M/Ex Hidden (2223).mp4"
vid "$M/Ex Hidden (2223).featurette.mp4"; vid "$M/Ex Hidden (2223)-featurettes.mp4"; vid "$M/Ex Hidden (2223).sample.mp4"
vid "$M/sample/Singular Folder.mp4"
M="$E/Ex Parts (2224)"
vid "$M/Ex Parts (2224) - part1.mp4"; vid "$M/Ex Parts (2224) - part2.mp4"; vid "$M/Ex Parts (2224) - part3.mp4"
M="$E/Ex Versions (2225)"
vid "$M/Ex Versions (2225).mp4"; vid "$M/Ex Versions (2225) - 4K.mp4"
M="$E/Ex Sub Folder (2226)"
vid "$M/Ex Sub Folder (2226).mp4"; vid "$M/featurette/Singular.mp4"; vid "$M/trailers/Real Trailer.mp4"
# A movie whose folder also holds a (non extras) sub folder is a Folder item.
M="$E/Ex Shape Plain (2227)"
vid "$M/Ex Shape Plain (2227).mp4"; aud "$M/soundtrack/01 Intro.mp3"; vid "$M/trailers/Plain Trailer.mp4"
aud "$M/theme.mp3"
M="$E/Ex Shape Root (2228)"
vid "$M/Ex Shape Root (2228).mp4"; aud "$M/soundtrack/01 Intro.mp3"; vid "$M/Ex Shape Root (2228)-trailer.mp4"
aud "$M/theme.mp3"

ES="$ROOT/ExtraShows"
S="$ES/Extra Show (2230)"
vid "$S/Season 01/Extra Show S01E01.mp4"
vid "$S/Season 01/Extra Show S01E02.mp4"
vid "$S/trailers/Show Trailer.mp4"; vid "$S/extras/Show Extra.mp4"; vid "$S/Extra Show (2230)-featurette.mp4"
aud "$S/theme.mp3"; vid "$S/backdrops/Show Loop.mp4"
vid "$S/Season 01/trailers/Season Trailer.mp4"; vid "$S/Season 01/extras/Season Extra.mp4"
vid "$S/Season 01/Extra Show S01E01-featurette.mp4"
aud "$S/Season 01/theme.mp3"

R="$ROOT/Resid"
# Season folders sharing a number, an episode-like folder, several files of
# one episode, numbering without a season/episode pattern.
S="$R/Dup Show (2240)"
vid "$S/Season 01/Dup Show S01E01.mp4"; vid "$S/Season 1 Bonus/Dup Show S01E02.mp4"
vid "$S/Specials/Dup Show S00E01.mp4"; vid "$S/Season 00/Dup Show S00E02.mp4"
S="$R/Folder Show (2241)"
vid "$S/Season 01/Folder Show S01E01.mp4"; vid "$S/S26E01/Folder Show S26E01.mp4"
S="$R/Version Show (2242)"
vid "$S/Season 01/Version Show S01E01.mp4"; vid "$S/Season 01/Version Show S01E01 1080p.mp4"
vid "$S/Season 01/Version Show S01E01 - 4K.mp4"; vid "$S/Season 01/Version Show S01E02.mp4"
S="$R/Number Show (2243)"
vid "$S/Season 01/Number Show E5E6.mp4"; vid "$S/Season 01/Number Show Title3.mp4"
vid "$S/Season 01/Number Show E05E06E07.mp4"
vid "$S/Season 01/Number Show Part3.mp4"; vid "$S/Season 01/Number Show x264.mp4"

# Photo extensions of a home-videos library (own library "PhotoTypes", lazy).
P="$ROOT/PhotoTypes/Album"
for e in jpg jpeg png gif bmp webp tif tiff avif ico cr2 dng nef arw; do pic "$P/photo.$e"; done
for e in heic heif jpe jfif svg psd jxl tga raw; do pic "$P/other.$e"; done

echo "media tree ready under $ROOT"
