package scan

import (
	"strconv"
	"strings"
	"unicode"
)

// This file derives a title and a production year from a file name the way
// the reference server does for a movie file that sits loose in a library
// (and for an episode file with no .nfo): the year and the release tokens a
// scene name carries end the title. The rules are what was observed on the
// 12.2 reference with online providers disabled (contract items.names.R9 to
// R12).

// cleanTokens are the release tokens that end a title when they stand
// between delimiters (compared in lower case).
var cleanTokens = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`3d sbs hsbs htab tab mvc hdr hdc uhd ultrahd 4k ac3 dts custom dc divx divx5
		dsr dsrip dutch dvd dvdrip dvdscr dvdscreener screener dvdivx cam fragment fs hdtv hdtvrip hdrip internal
		limited ntsc ogg ogm pal pdtv proper repack rerip retail cd1 cd2 cd3 cd4 cd5 cd6 cd7 cd8 cd9 r5 bd5 bd bdrip
		brrip se svcd swedish german read.nfo nfofix unrated ws telesync ts telecine tc 480p 480i 576p 576i 720p
		720i 1080p 1080i 2160p hrhd hrhdtv hddvd bluray x264 h264 x265 h265 xvid xvidvd xxx web-dl blu-ray aac multi`) {
		cleanTokens[t] = true
		cleanTokenList = append(cleanTokenList, t)
	}
}

var cleanTokenList []string

// isCleanDelim reports whether c separates a token or a year from the text
// around it.
func isCleanDelim(c byte) bool {
	switch c {
	case ' ', '_', ',', '.', '(', ')', '[', ']', '-':
		return true
	}
	return false
}

// yearEnd reports whether the text at i is a four digit year in
// [1900, 2099] that is not followed by another digit, returning its value.
func yearAt(s string, i int) (int, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	if i+4 < len(s) && s[i+4] >= '0' && s[i+4] <= '9' {
		return 0, false
	}
	for k := 0; k < 4; k++ {
		if s[i+k] < '0' || s[i+k] > '9' {
			return 0, false
		}
	}
	y, err := strconv.Atoi(s[i : i+4])
	if err != nil || y < 1900 || y > 2099 {
		return 0, false
	}
	return y, true
}

// lastYear finds the last year in s that follows a delimiter (a year at the
// very start of the text is not one). It returns the year and the index of
// the delimiter before it, or ok false.
func lastYear(s string) (year, delim int, ok bool) {
	for i := len(s) - 4; i >= 2; i-- {
		if !isCleanDelim(s[i-1]) {
			continue
		}
		if y, found := yearAt(s, i); found {
			return y, i - 1, true
		}
	}
	return 0, 0, false
}

// cutAtToken returns the byte index of the delimiter that precedes the first
// release token of s, or -1. A token matches only as a whole: a delimiter
// (or the end of the text) must follow it.
func cutAtToken(s string) int {
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		if !isCleanDelim(lower[i]) {
			continue
		}
		rest := lower[i+1:]
		for _, t := range cleanTokenList {
			if !strings.HasPrefix(rest, t) {
				continue
			}
			if len(rest) == len(t) || isCleanDelim(rest[len(t)]) {
				return i
			}
		}
	}
	return -1
}

// trimTrailingGroups removes bracket groups ("[x]") that end the text.
func trimTrailingGroups(s string) string {
	for {
		t := strings.TrimRightFunc(s, unicode.IsSpace)
		if !strings.HasSuffix(t, "]") {
			return s
		}
		open := strings.LastIndexByte(t, '[')
		if open < 0 {
			return s
		}
		s = strings.TrimRightFunc(t[:open], unicode.IsSpace)
	}
}

// CleanTitle returns the title a file name stands for and the production
// year found in it (0 when none): the text before the last year (when
// cutYear), then before the first release token, without trailing bracket
// groups. A name that nothing ends is returned exactly as written, white
// space included.
func CleanTitle(name string, cutYear bool) (title string, year int) {
	s := name
	cut := false
	if y, d, ok := lastYear(s); ok {
		year = y
		if cutYear {
			s = strings.TrimRight(s[:d], " -")
			cut = true
		}
	}
	if i := cutAtToken(s); i >= 0 {
		s = strings.TrimRight(s[:i], " ")
		cut = true
	}
	if t := trimTrailingGroups(s); t != s {
		s = t
		cut = true
	}
	if !cut {
		return name, year
	}
	return s, year
}
