package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// EpisodeFile is what an episode file name says about the episode, as the
// reference server reads it with online providers disabled (contract
// items.episodes.R6 to R9). Every number is nil when the name carries none.
type EpisodeFile struct {
	Season, Episode, EpisodeEnd *int
	// Date is the air date a name such as "Show 2020-01-31" carries.
	Date *time.Time
	// Year is the production year read from the name (0 when none).
	Year int
	// Name is the title the file name stands for: the name without its
	// extension, ended at the last year and at the first release token,
	// without trailing bracket groups and without a trailing " - 05".
	Name string
}

var (
	// "S01E02", "s01.e02", "S1 - E2", "[S01E02]": season, episode and the
	// episodes chained to it ("E03E04", "-E06", "-06").
	reSeasonEpisode = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])s(\d{1,4})[ ._-]*e(\d{1,4})((?:e\d{1,4})*)(?:-e?(\d{1,4}))?`)
	// "1x02", "2x05-2x06", "1x2x3".
	reCrossEpisode = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])(\d{1,4})x(\d{1,4})((?:x\d{1,4})*)(?:-(?:\d{1,4}x)?(\d{1,4}))?`)
	// "Season 1 Episode 2".
	reSeasonWords = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])season[ ._-]+(\d{1,4})[ ._-]+episode[ ._-]+(\d{1,4})`)
	// "2020-01-31", "2020.01.31", "2020 01 31".
	reDateYMD = regexp.MustCompile(`(?:^|[ ._\-\[(])((?:19|20)\d{2})[-. ](\d{2})[-. ](\d{2})(?:$|\D)`)
	// "31-01-2020" and "01-31-2020".
	reDateDMY = regexp.MustCompile(`(?:^|[ ._\-\[(])(\d{2})[-. ](\d{2})[-. ]((?:19|20)\d{2})(?:$|\D)`)
	// "Ep05", "EP5", "Episode05": the number follows without a separator.
	reEpisodeWord = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])ep(?:isode)?(\d{1,4})`)
	// "E05", "e05.Title" (a separator or the end must follow).
	reEpisodeLetter = regexp.MustCompile(`(?i)(?:^|[ ._-])e(\d{1,4})(?:[._-]|$)`)
	// "Part 2", "Pt.2", "part_3".
	rePart = regexp.MustCompile(`(?i)(?:^|[ ._-])p(?:ar)?t[._ -](\d{1,4})`)
	// "[01]".
	reBracketNumber = regexp.MustCompile(`\[(\d{1,4})\]`)
	// "Show - 05", "Show - 105 - Title": at most three digits.
	reDashNumber = regexp.MustCompile(`^[^\d]*?\S\s-\s(\d{1,3})`)
	// A trailing " - 05" that ends the name.
	reTrailingDash = regexp.MustCompile(`\s-\s\d+$`)
)

func atoi(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// lastNumber returns the last number of a chained "e03e04"/"x3x4" tail.
func lastChained(tail string) *int {
	if tail == "" {
		return nil
	}
	i := len(tail)
	for i > 0 && tail[i-1] >= '0' && tail[i-1] <= '9' {
		i--
	}
	return atoi(tail[i:])
}

// bareNumber finds the first run of digits (at most four are read) that
// starts the name or follows a space that follows a letter, a digit or a
// hyphen ("Show 05", "05 - Title", "Show A 5"; not "Show (2019) 05",
// "Ep. 5" or "Show_05").
func bareNumber(s string) *int {
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] < '0' || rs[i] > '9' {
			continue
		}
		if i > 0 && rs[i-1] >= '0' && rs[i-1] <= '9' {
			continue
		}
		ok := i == 0
		if !ok && rs[i-1] == ' ' {
			j := i - 1
			for j > 0 && rs[j] == ' ' {
				j--
			}
			if rs[j] != ' ' {
				c := rs[j]
				ok = unicode.IsLetter(c) || (c >= '0' && c <= '9') || c == '-'
			}
		}
		if !ok {
			continue
		}
		j := i
		for j < len(rs) && j-i < 4 && rs[j] >= '0' && rs[j] <= '9' {
			j++
		}
		return atoi(string(rs[i:j]))
	}
	return nil
}

// parseNameDate reads a date in a name: year-month-day ("2020-01-31", kept in
// the title) or day-month-year ("31-01-2020"; the year ends the title). A
// numeric date group whose month is out of range ("01-31-2020") still
// counts as a date: it gives the year but no air date and no numbers.
func parseNameDate(s string) (t *time.Time, year int, keepName, ok bool) {
	if m := reDateYMD.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			v := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			return &v, y, true, true
		}
	}
	if m := reDateDMY.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			v := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			return &v, y, false, true
		}
		return nil, y, false, true
	}
	return nil, 0, false, false
}

// ParseEpisodeFile reads the numbering and the title out of an episode file
// name (with or without its extension). The numbering forms are tried in this
// order and the first that matches decides: season and episode ("S01E02",
// "1x02", "Season 1 Episode 2"), a date, an episode word ("Ep05"), "E05",
// "Part 2", "[01]", " - 05", then the first bare number.
func ParseEpisodeFile(filename string) EpisodeFile {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	var ef EpisodeFile
	keepName := false
	switch {
	case reSeasonEpisode.MatchString(base):
		m := reSeasonEpisode.FindStringSubmatch(base)
		ef.Season, ef.Episode = atoi(m[1]), atoi(m[2])
		if end := lastChained(m[3]); end != nil {
			ef.EpisodeEnd = end
		} else if m[4] != "" {
			ef.EpisodeEnd = atoi(m[4])
		}
	case reCrossEpisode.MatchString(base):
		m := reCrossEpisode.FindStringSubmatch(base)
		ef.Season, ef.Episode = atoi(m[1]), atoi(m[2])
		if end := lastChained(m[3]); end != nil {
			ef.EpisodeEnd = end
		} else if m[4] != "" {
			ef.EpisodeEnd = atoi(m[4])
		}
	case reSeasonWords.MatchString(base):
		m := reSeasonWords.FindStringSubmatch(base)
		ef.Season, ef.Episode = atoi(m[1]), atoi(m[2])
	default:
		if t, y, keep, ok := parseNameDate(base); ok {
			ef.Date, ef.Year, keepName = t, y, keep
			break
		}
		for _, re := range []*regexp.Regexp{reEpisodeWord, reEpisodeLetter, rePart, reBracketNumber, reDashNumber} {
			if m := re.FindStringSubmatch(base); m != nil {
				ef.Episode = atoi(m[1])
				break
			}
		}
		if ef.Episode == nil {
			ef.Episode = bareNumber(base)
		}
	}

	name, year := CleanTitle(base, !keepName)
	if ef.Year == 0 {
		ef.Year = year
	}
	if !keepName && reTrailingDash.MatchString(name) {
		name = strings.TrimSpace(reTrailingDash.ReplaceAllString(name, ""))
	}
	if name == "" {
		name = strings.TrimSpace(base)
	}
	ef.Name = name
	return ef
}
