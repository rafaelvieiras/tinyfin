// nameparse.go extracts identification candidates (title, year, season/
// episode numbers) purely from filesystem paths, following the naming
// convention Kodi/Jellyfin both document publicly (kodi.wiki's "Naming
// video files" guide — a third-party convention independent projects
// converge on, not something specific to or derived from the reference
// server's own source). This is what makes an item browsable/playable
// with correct-enough baseline metadata even with no metadata.Provider
// configured (docs/08): Scan calls this before ever calling a Provider,
// and a Provider's Result only ever overlays on top of what this already
// found.
package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// yearSuffix matches a trailing " (YYYY)" on a movie/series folder name —
// e.g. "The Matrix (1999)" — the same convention devseed's throwaway
// parseNameYear used for movies, generalized here to also apply to series
// root folders.
var yearSuffix = regexp.MustCompile(`^(.*) \((\d{4})\)$`)

// ParseTitleYear splits "Title (Year)" into its two parts. If name doesn't
// match that shape, title is the whole name unchanged and year is 0.
func ParseTitleYear(name string) (title string, year int) {
	if m := yearSuffix.FindStringSubmatch(name); m != nil {
		if y, err := strconv.Atoi(m[2]); err == nil {
			return m[1], y
		}
	}
	return name, 0
}

// seasonFolder matches a season directory name: a season word (English and
// the other languages observed on the reference) followed by the number
// with any separator or none ("Season 1", "Season06", "Season_7",
// "Staffel 10", "Series 11"), optionally followed by more text after a
// non-alphanumeric ("Season 15 - Title", "Season 21 (2010)"); "S4" and
// "S17 Complete"; or the bare number ("12"). Case-insensitive.
var seasonFolder = regexp.MustCompile(`(?i)^\s*(?:(?:season|series|staffel|temporada|saison|stagione|seizoen)[\s._-]*(\d{1,5})(?:[^\p{L}\d].*)?|s(\d{1,4})(?:[\s._-].*)?|(\d{1,4}))\s*$`)

// pureSeason matches a folder name that is only a season ("Season 3", "S04", "12").
var pureSeason = regexp.MustCompile(`(?i)^\s*(?:(?:season|series|staffel|temporada|saison|stagione|seizoen)[\s._-]*\d{1,5}|s\d{1,4}|\d{1,4})\s*$`)

// specialsFolder matches the folder name that stands for season 0.
var specialsFolder = regexp.MustCompile(`(?i)^\s*Specials\s*$`)

// ParseSeasonFolder returns a season directory's number and whether name
// actually matched the convention (so a caller can tell "season 0" from
// "not a season folder at all").
func ParseSeasonFolder(name string) (season int, ok bool) {
	if specialsFolder.MatchString(name) {
		return 0, true
	}
	m := seasonFolder.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	for _, g := range m[1:] {
		if g == "" {
			continue
		}
		n, err := strconv.Atoi(g)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// episodePattern matches the two dominant Kodi/Jellyfin episode-numbering
// conventions found in filenames: "S01E02" (SxxEyy, any digit count) and
// "1x02" (season x episode). Both are matched case-insensitively and
// anywhere in the filename, since real files commonly prefix the pattern
// with the series name and suffix it with an episode title
// ("Show Name - S01E02 - Episode Title.mkv").
var episodePattern = regexp.MustCompile(`(?i)s(\d{1,4})[ ._-]?e(\d{1,4})|(\d{1,4})x(\d{1,4})`)

// episodeOnlyPattern matches a bare episode number: "E04", "Ep 03",
// "Episode 8" delimited by separators or brackets.
var episodeOnlyPattern = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])(?:episode|ep|e)[ ._-]?(\d{1,4})(?:$|[ ._\-\])])`)

// leadingNumberPattern matches a file name that starts with an episode
// number: "01 - Title", "12".
var leadingNumberPattern = regexp.MustCompile(`^(\d{1,3})(?:[ ._-]|$)`)

// ParseEpisodeNumbers extracts the season and episode numbers a file name
// carries; either may be absent. A pattern with both numbers (S01E02, 1x02)
// gives both; the episode-only forms ("E04", "Ep 03", "01 - Title", "12")
// give just the episode. A name with no number at all gives neither.
func ParseEpisodeNumbers(filename string) (season, episode *int) {
	if s, e, _, ok := ParseEpisode(filename); ok {
		return &s, &e
	}
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	for _, re := range []*regexp.Regexp{episodeOnlyPattern, leadingNumberPattern} {
		if m := re.FindStringSubmatch(name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return nil, &n
			}
		}
	}
	return nil, nil
}

// ParseSeriesTitleYear splits a series folder name into title and year. The
// whole name is trimmed first and the white space before "(YYYY)" dropped,
// so " Show  (2012) " gives ("Show", 2012).
func ParseSeriesTitleYear(name string) (title string, year int) {
	t := strings.TrimFunc(name, unicode.IsSpace)
	if m := seriesYearSuffix.FindStringSubmatch(t); m != nil {
		if y, err := strconv.Atoi(m[2]); err == nil {
			return strings.TrimFunc(m[1], unicode.IsSpace), y
		}
	}
	return t, 0
}

var seriesYearSuffix = regexp.MustCompile(`^(.*?)\s*\((\d{4})\)$`)

// ParseEpisode extracts (season, episode, episodeTitle) from a filename
// (extension included; stripped internally). ok is false if filename
// doesn't contain either recognized pattern at all — callers should treat
// that file as not a recognizable episode (skip it, per docs/08's "the
// minimum needed to serve a browsable catalog" scope — this project does
// not attempt looser heuristics beyond the documented convention).
//
// episodeTitle is whatever text follows the matched pattern, with leading
// separators (" - ", ".", "_") trimmed — best-effort, overwritten by a
// configured Provider's Result.Title when one matches, same as every
// other filename-derived field (Scan's own doc comment).
func ParseEpisode(filename string) (season, episode int, episodeTitle string, ok bool) {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	loc := episodePattern.FindStringSubmatchIndex(name)
	if loc == nil {
		return 0, 0, "", false
	}
	m := episodePattern.FindStringSubmatch(name)

	var seasonStr, episodeStr string
	if m[1] != "" {
		seasonStr, episodeStr = m[1], m[2]
	} else {
		seasonStr, episodeStr = m[3], m[4]
	}
	s, err1 := strconv.Atoi(seasonStr)
	e, err2 := strconv.Atoi(episodeStr)
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}

	rest := name[loc[1]:]
	rest = strings.TrimLeft(rest, " -._")
	return s, e, rest, true
}

// videoExtensions is the set of file extensions Scan treats as a playable
// media file, matching devseed's throwaway list plus the couple of extra
// containers real libraries commonly hold.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".webm": true, ".m4v": true, ".ts": true, ".mov": true,
	".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true, ".ogv": true, ".3gp": true, ".m2ts": true,
	".vob": true, ".divx": true,
	// disc images (VideoType Iso)
	".iso": true, ".img": true,
	// a text file holding the address (or path) of the media
	".strm": true,
}

// extraSuffixes end the base name of a file that is a trailer or a sample
// rather than a video of its own; the bare words count too.
var extraSuffixes = []string{"trailer", "sample"}

// IsExtraVideo reports whether name is a trailer or a sample clip by its
// name ("Movie-trailer.mp4", "Movie.sample.mkv", "sample.mp4").
func IsExtraVideo(name string) bool {
	base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	for _, w := range extraSuffixes {
		if base == w {
			return true
		}
		for _, sep := range []string{"-", ".", "_", " "} {
			if strings.HasSuffix(base, sep+w) {
				return true
			}
		}
	}
	return false
}

// ignoredFolderNames are folders no scanner walks into: sidecar and
// recycle-bin directories that file managers and NAS appliances create.
var ignoredFolderNames = map[string]bool{"@eadir": true, "#recycle": true, "metadata": true}

// IgnoredFolder reports whether a folder is left out of a home-videos scan:
// hidden folders (leading dot) and sidecar or recycle-bin folders.
func IgnoredFolder(name string) bool {
	return strings.HasPrefix(name, ".") || ignoredFolderNames[strings.ToLower(name)]
}

// extrasFolderNames are folders that are listed but whose content is not
// scanned as videos of their own.
var extrasFolderNames = map[string]bool{"extras": true, "sample": true, "backdrops": true}

// ExtrasFolder reports whether a folder's content is left out of a
// home-videos scan while the folder itself is still listed.
func ExtrasFolder(name string) bool {
	return extrasFolderNames[strings.ToLower(name)]
}

// IsVideoFile reports whether name's extension is one Scan treats as a
// media file to catalog, case-insensitively.
func IsVideoFile(name string) bool {
	return videoExtensions[strings.ToLower(filepath.Ext(name))]
}

// skippedFolderNames are folders no video scan walks into (sidecar, recycle
// and extras folders): the reference leaves them out of a series or movie
// library entirely.
var skippedFolderNames = map[string]bool{
	"extras": true, "extra": true, "clips": true, "trailers": true, "backdrops": true, "theme-music": true,
	"sample": true, "samples": true, "featurettes": true, "other": true, "subs": true,
	"behind the scenes": true, "deleted scenes": true, "interviews": true, "scenes": true, "shorts": true,
	"extrafanart": true, "extrathumbs": true,
	"metadata": true, "@eadir": true, "#recycle": true, "lost+found": true,
}

// SkippedFolder reports whether a folder is left out of a movie or series
// library: hidden folders and the names above, in any case.
func SkippedFolder(name string) bool {
	return strings.HasPrefix(name, ".") || skippedFolderNames[strings.ToLower(name)]
}

// photoExtensions are the picture file extensions a home-videos library
// catalogs as photos.
var photoExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true, ".webp": true,
	".tif": true, ".tiff": true, ".avif": true, ".ico": true, ".wbmp": true,
	// camera raw formats the reference reads as pictures
	".cr2": true, ".crw": true, ".dng": true, ".arw": true, ".erf": true, ".nef": true,
	".nrw": true, ".orf": true, ".pef": true, ".raf": true, ".rw2": true,
}

// IsPhotoFile reports whether name has a picture extension (any case).
func IsPhotoFile(name string) bool {
	return photoExtensions[strings.ToLower(filepath.Ext(name))]
}

// artworkName matches the base names of folder artwork ("folder.jpg",
// "poster.png", "fanart.jpg", "backdrop1.jpg"), which are images of the
// folder, not photos.
var artworkName = regexp.MustCompile(`(?i)^(folder|poster|cover|default|thumb|thumbnail|landscape|logo|fanart|backdrop)\d*$`)

// isPhotoEntry reports whether the picture called name is a photo: not
// hidden, not folder artwork and not the image of a video (named like it, or
// like it plus a "-" suffix such as "-poster"); videoBases are the lower-case
// base names of the folder's videos.
func isPhotoEntry(name string, videoBases map[string]bool) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if artworkName.MatchString(base) {
		return false
	}
	lower := strings.ToLower(base)
	for vb := range videoBases {
		if lower == vb || strings.HasPrefix(lower, vb+"-") {
			return false
		}
	}
	return true
}
