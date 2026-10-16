package storage

import (
	"fmt"
	"strings"
	"unicode"
)

// NormalizeSortName derives the key items are ordered by (and that the
// SortName field carries) from a title (contract: items.sort.R8):
//
//  0. surrounding white space (any Unicode space) is dropped;
//  1. the articles "the", "a" and "an" (any case) are removed as whole
//     words: at the start when a space follows, between two spaces, and at
//     the end when a space precedes, in that order for each article;
//  2. the characters , & - { } ' are removed and . + % become a space;
//  3. every run of decimal digits (BMP, any script) shorter than ten digits
//     is zero-padded to ten, so numbers order numerically under a plain byte
//     comparison;
//  4. the text is lower-cased and transliterated to Latin letters (see
//     transliterate): accents, ligatures, Greek, Cyrillic, Hebrew, Arabic,
//     Indic scripts, Thai, kana, Hangul and Han readings. When a character
//     outside the set the reference leaves alone is present, ASCII
//     punctuation such as ! ( ) ? : ; is dropped as well.
func NormalizeSortName(title string) string {
	// Surrounding white space never takes part in the key.
	s := strings.TrimFunc(title, unicode.IsSpace)
	for _, art := range [...]string{"the", "a", "an"} {
		s = removeArticle(s, art)
	}
	return sortKeyCore(s)
}

// SortSearchKey is the sort-name form of a search term: NormalizeSortName
// without the removal of leading articles (a search for "a b" must not
// become a search for "b").
func SortSearchKey(term string) string {
	return sortKeyCore(strings.TrimFunc(term, unicode.IsSpace))
}

// sortKeyCore is steps 2 to 4 of NormalizeSortName.
func sortKeyCore(s string) string {
	// Steps 2 and 3 work on code points; the transliteration needs the
	// whole string (it depends on neighbours), so it runs afterwards.
	work := make([]rune, 0, len(s)+16)
	var run []rune
	emitRun := func() {
		if len(run) == 0 {
			return
		}
		for i := len(run); i < 10; i++ {
			work = append(work, '0')
		}
		work = append(work, run...)
		run = run[:0]
	}
	for _, r := range s {
		if r <= 0xFFFF && unicode.IsDigit(r) {
			run = append(run, r)
			continue
		}
		switch r {
		case ',', '&', '-', '{', '}', '\'':
			continue // deleted: the digits around it still form one run
		case '.', '+', '%':
			emitRun()
			work = append(work, ' ')
			continue
		}
		emitRun()
		work = append(work, r)
	}
	emitRun()
	return transliterate(work)
}

// SortKey is the value stored as the item's sort name (items.sort.R8). An
// explicit sort title (ForcedSortName) is normalized on its own for every
// type but seasons. Otherwise an episode orders by season, then episode
// number, then its own title, which is kept as written; the parts that are
// unknown drop out (items.sort.R9). A season orders by its number, a
// season without one by its name as written; everything else by the
// normalized title.
func (it Item) SortKey() string {
	if it.ForcedSortName != "" && it.Type != ItemTypeSeason {
		if k := NormalizeSortName(it.ForcedSortName); k != "" {
			return k
		}
	}
	raw := it.SortName
	if raw == "" {
		raw = it.Name
	}
	switch it.Type {
	case ItemTypeSeason:
		if it.IndexNumber != nil {
			return fmt.Sprintf("%04d", *it.IndexNumber)
		}
		return raw
	case ItemTypeAudio:
		// A track keeps the "disc - track - title" form the scan built
		// (music.track.R4), as an episode does.
		return raw
	case ItemTypeEpisode:
		switch {
		case it.ParentIndexNumber != nil && it.IndexNumber != nil:
			return fmt.Sprintf("%03d - %04d - %s", *it.ParentIndexNumber, *it.IndexNumber, raw)
		case it.ParentIndexNumber != nil:
			return fmt.Sprintf("%03d - %s", *it.ParentIndexNumber, raw)
		case it.IndexNumber != nil:
			return fmt.Sprintf("%04d - %s", *it.IndexNumber, raw)
		}
		return raw
	}
	return NormalizeSortName(raw)
}

// removeArticle drops the ASCII word art (any case) from s the way the sort
// key does: from the start when a space follows, from every position
// between two spaces (the spaces collapse into one), and from the end when
// a space precedes it. A string that is only the article is kept.
func removeArticle(s, art string) string {
	n := len(art)
	if len(s) > n && s[n] == ' ' && strings.EqualFold(s[:n], art) {
		s = s[n+1:]
	}
	// " art " -> " ", left to right without overlap.
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == ' ' && i+n+2 <= len(s) && s[i+n+1] == ' ' && strings.EqualFold(s[i+1:i+1+n], art) {
			b.WriteByte(' ')
			i += n + 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	s = b.String()
	if len(s) > n && s[len(s)-n-1] == ' ' && strings.EqualFold(s[len(s)-n:], art) {
		s = s[:len(s)-n-1]
	}
	return s
}
