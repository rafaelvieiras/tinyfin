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
//  3. the result is lower-cased and Latin letters are folded to ASCII
//     (accents, ligatures, ß, ø, ł, ...);
//  4. every run of ASCII digits shorter than ten digits is zero-padded to ten,
//     so numbers order numerically under a plain byte comparison.
//
// Scripts other than Latin are left as they are.
func NormalizeSortName(title string) string {
	s := title
	lower := strings.ToLower(s)
	for _, art := range [...]string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, art) && len(s) > len(art) {
			s = s[len(art):]
			break
		}
	}

	var b strings.Builder
	b.Grow(len(s) + 16)
	var run []byte
	emitRun := func() {
		if len(run) == 0 {
			return
		}
		for i := len(run); i < 10; i++ {
			b.WriteByte('0')
		}
		b.Write(run)
		run = run[:0]
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			run = append(run, byte(r))
			continue
		}
		emitRun()
		switch r {
		case ',', '&', '-', '{', '}', '\'':
			continue
		case '.', '+', '%':
			b.WriteByte(' ')
			continue
		}
		r = unicode.ToLower(r)
		if rep, ok := latinFold[r]; ok {
			b.WriteString(rep)
			continue
		}
		b.WriteRune(r)
	}
	emitRun()
	return b.String()
}

// latinFold maps lower-case Latin letters with diacritics, ligatures and
// the handful of letters that have no decomposition to their ASCII
// spelling. The reference transliterates every script; Tinyfin only folds
// Latin (no dependency on a Unicode tables module).
var latinFold = func() map[rune]string {
	m := map[rune]string{}
	for ascii, group := range map[string]string{
		"a": "àáâãäåāăąǎǟǡǻȁȃȧ",
		"c": "çćĉċč",
		"d": "ďđð",
		"e": "èéêëēĕėęěȅȇȩ",
		"g": "ĝğġģǧǵ",
		"h": "ĥħ",
		"i": "ìíîïĩīĭįıǐȉȋ",
		"j": "ĵ",
		"k": "ķǩ",
		"l": "ĺļľŀł",
		"n": "ñńņňǹ",
		"o": "òóôõöøōŏőǒǫȍȏ",
		"r": "ŕŗřȑȓ",
		"s": "śŝşšș",
		"t": "ţťŧț",
		"u": "ùúûüũūŭůűųǔȕȗ",
		"w": "ŵ",
		"y": "ýÿŷȳ",
		"z": "źżž",
	} {
		for _, r := range group {
			m[r] = ascii
		}
	}
	for r, rep := range map[rune]string{
		'æ': "ae", 'œ': "oe", 'ß': "ss", 'þ': "th", 'ĳ': "ij",
		'ǆ': "dz", 'ǉ': "lj", 'ǌ': "nj",
		'ﬀ': "ff", 'ﬁ': "fi", 'ﬂ': "fl", 'ﬃ': "ffi", 'ﬄ': "ffl",
	} {
		m[r] = rep
	}
	return m
}()

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
