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

// seasonFolder matches a season directory name — "Season 01", "Season 1",
// "Season 00" (specials) — the Kodi/Jellyfin convention's standard season
// folder shape. Case-insensitive: real libraries are inconsistent about
// casing.
var seasonFolder = regexp.MustCompile(`(?i)^Season\s+(\d{1,4})$`)

// ParseSeasonFolder returns a season directory's number and whether name
// actually matched the convention (so a caller can tell "season 0" from
// "not a season folder at all").
func ParseSeasonFolder(name string) (season int, ok bool) {
	m := seasonFolder.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// episodePattern matches the two dominant Kodi/Jellyfin episode-numbering
// conventions found in filenames: "S01E02" (SxxEyy, any digit count) and
// "1x02" (season x episode). Both are matched case-insensitively and
// anywhere in the filename, since real files commonly prefix the pattern
// with the series name and suffix it with an episode title
// ("Show Name - S01E02 - Episode Title.mkv").
var episodePattern = regexp.MustCompile(`(?i)s(\d{1,4})e(\d{1,4})|(\d{1,4})x(\d{1,4})`)

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
}

// IsVideoFile reports whether name's extension is one Scan treats as a
// media file to catalog, case-insensitively.
func IsVideoFile(name string) bool {
	return videoExtensions[strings.ToLower(filepath.Ext(name))]
}
