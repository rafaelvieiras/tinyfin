package storage

import (
	"fmt"
	"strings"
	"unicode"
)

// NormalizeSortName derives the key items are ordered by (and that the
// SortName field carries) from a title (contract: items.sort.R8):
//
//  1. one leading article ("the ", "a ", "an ", any case) is dropped, unless
//     that would leave nothing;
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
	s := title
	lower := strings.ToLower(s)
	for _, art := range [...]string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, art) && len(s) > len(art) {
			s = s[len(art):]
			break
		}
	}

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
// episode orders by season, then episode number, then its own title, which
// is kept as written; a season by its number; everything else by the
// normalized title.
func (it Item) SortKey() string {
	raw := it.SortName
	if raw == "" {
		raw = it.Name
	}
	switch it.Type {
	case ItemTypeSeason:
		if it.IndexNumber != nil {
			return fmt.Sprintf("%04d", *it.IndexNumber)
		}
	case ItemTypeEpisode:
		if it.ParentIndexNumber != nil && it.IndexNumber != nil {
			return fmt.Sprintf("%03d - %04d - %s", *it.ParentIndexNumber, *it.IndexNumber, raw)
		}
	}
	return NormalizeSortName(raw)
}
