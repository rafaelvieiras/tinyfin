package transcode

import (
	"bytes"
	"unicode/utf8"
)

// detectCharset names the text encoding of a subtitle file the way the
// "charenc" option of the subtitles filter wants it, or "" when none should be
// given (an encoding marked by a byte order mark other than UTF-8's or UTF-32's
// is read by the filter itself). It is a heuristic over the bytes standing in
// for the statistical detector of the reference: it tells apart ASCII, UTF-8,
// the CJK double-byte encodings, the single-byte scripts (Cyrillic, Greek,
// Hebrew, Arabic, Thai) by their commonest letters and the Latin code pages by
// the bytes they use inside words. Short and ambiguous samples can still differ
// from the reference (see bitmap.md).
func detectCharset(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return "utf-8"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE, 0x00, 0x00}):
		return "utf-32le"
	case bytes.HasPrefix(data, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return "utf-32be"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return ""
	}
	high, letters := 0, 0
	for _, b := range data {
		switch {
		case b >= 0x80:
			high++
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z':
			letters++
		}
	}
	if high == 0 {
		return "ascii"
	}
	if utf8.Valid(data) {
		// a UTF-8 verdict needs a few multi-byte characters behind it; one or
		// two could as well be a single-byte code page
		if multiByteRunes(data) >= 3 {
			return "utf-8"
		}
		return "windows-1250"
	}
	if cjk := detectCJK(data, high); cjk != "" {
		return cjk
	}
	if high*10 >= (high+letters)*3 && highRuns(data, high) {
		if enc := detectScript(data, high); enc != "" {
			return enc
		}
	}
	return detectLatin(data)
}

// multiByteRunes counts the characters of valid UTF-8 data that take more than
// one byte.
func multiByteRunes(data []byte) int {
	n := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r >= 0x80 {
			n++
		}
		data = data[size:]
	}
	return n
}

// detectCJK recognises Shift-JIS, EUC-JP, EUC-KR, GB18030 and Big5 by the
// shape of their pairs; "" when the data is none of them.
func detectCJK(data []byte, high int) string {
	if validDoubleByte(data, 0x81, 0x9F, 0xE0, 0xFC, 0x40, 0xFC) && hasKana(data) {
		return "shift-jis"
	}
	pairs, lowTrail, kana, level1, big5Lead, punct := 0, 0, 0, 0, 0, 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < 0x80 {
			continue
		}
		if c < 0xA1 || c > 0xF9 || i+1 >= len(data) || data[i+1] < 0x40 || data[i+1] == 0x7F || data[i+1] == 0xFF {
			return ""
		}
		pairs++
		if data[i+1] < 0xA1 {
			lowTrail++
		}
		if c == 0xA4 || c == 0xA5 {
			kana++
		}
		if c == 0xA1 || c == 0xA3 {
			punct++ // ideographic and full-width punctuation
		}
		if c >= 0xB0 && c <= 0xD7 {
			level1++
		}
		if c >= 0xA1 && c <= 0xC6 {
			big5Lead++
		}
		i++
	}
	if pairs < 2 {
		return ""
	}
	pairs -= punct
	if pairs < 2 {
		return ""
	}
	switch {
	case lowTrail > 0 && (big5Lead-punct)*100 >= pairs*85:
		return "big5"
	case lowTrail > 0 && pairs >= 4 && level1*100 >= pairs*85:
		return "gb18030"
	case lowTrail > 0:
		return ""
	case kana*4 >= pairs:
		return "euc-jp"
	case level1*100 >= pairs*85:
		return eucKRorGB(data)
	case (big5Lead-punct)*100 >= pairs*85:
		return "big5"
	}
	return ""
}

// eucKRorGB tells Korean from Chinese text whose pairs all fit the GB2312
// level-1 range: hangul syllables have their leads in 0xB0-0xC8, Chinese text
// reaches past it.
func eucKRorGB(data []byte) string {
	for i := 0; i+1 < len(data); i++ {
		if data[i] >= 0x80 {
			if data[i] > 0xC8 {
				return "gb18030"
			}
			i++
		}
	}
	return "euc-kr"
}

// validDoubleByte reports whether every high byte of data starts a pair of a
// lead byte in one of the two ranges and a trail byte in [t0, t1].
func validDoubleByte(data []byte, a0, a1, b0, b1, t0, t1 byte) bool {
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < 0x80 {
			continue
		}
		if !(c >= a0 && c <= a1) && !(b1 != 0 && c >= b0 && c <= b1) {
			return false
		}
		if i+1 >= len(data) || data[i+1] < t0 || data[i+1] > t1 {
			return false
		}
		i++
	}
	return true
}

// hasKana reports whether data holds a Shift-JIS hiragana or katakana pair.
func hasKana(data []byte) bool {
	for i := 0; i+1 < len(data); i++ {
		if (data[i] == 0x82 && data[i+1] >= 0x9F && data[i+1] <= 0xF1) || (data[i] == 0x83 && data[i+1] >= 0x40 && data[i+1] <= 0x96) {
			return true
		}
	}
	return false
}

// scriptCandidate is a single-byte encoding of a non-Latin script with the
// bytes of its commonest letters.
type scriptCandidate struct {
	name   string
	common [256]bool
}

func mkCandidate(name string, bs ...byte) scriptCandidate {
	c := scriptCandidate{name: name}
	for _, b := range bs {
		c.common[b] = true
	}
	return c
}

// runeIndex is the position of r among the characters (not bytes) of s.
func runeIndex(s string, r rune) int {
	for i, c := range []rune(s) {
		if c == r {
			return i
		}
	}
	return -1
}

// cyr returns the bytes of the commonest Russian letters in an encoding given
// the byte of each letter of the alphabet "абвгдежзийклмнопрстуфхцчшщъыьэюя".
func cyr(enc func(r rune) byte) []byte {
	var out []byte
	for _, r := range "оеаинтсрвлкмдпуяыьгз" {
		out = append(out, enc(r))
	}
	return out
}

var scriptCandidates = func() []scriptCandidate {
	const alpha = "абвгдежзийклмнопрстуфхцчшщъыьэюя"
	idx := func(r rune) int { return runeIndex(alpha, r) }
	// letters in KOI8-R order
	const koi = "юабцдефгхийклмнопярстужвьызшэщчъ"
	return []scriptCandidate{
		mkCandidate("windows-1251", cyr(func(r rune) byte { return 0xE0 + byte(idx(r)) })...),
		mkCandidate("koi8-r", cyr(func(r rune) byte { return 0xC0 + byte(runeIndex(koi, r)) })...),
		mkCandidate("iso-8859-5", cyr(func(r rune) byte { return 0xD0 + byte(idx(r)) })...),
		mkCandidate("ibm866", cyr(func(r rune) byte {
			if i := idx(r); i < 16 {
				return 0xA0 + byte(i)
			} else {
				return 0xE0 + byte(i-16)
			}
		})...),
		// α ο ε ι τ ν σ ς ρ υ π κ μ λ η
		mkCandidate("iso-8859-7", 0xE1, 0xEF, 0xE5, 0xE9, 0xF4, 0xED, 0xF3, 0xF2, 0xF1, 0xF5, 0xF0, 0xEA, 0xEC, 0xEB, 0xE7),
		// י ו ה ל א ר ת מ ב ש נ
		mkCandidate("windows-1255", 0xE9, 0xE5, 0xE4, 0xEC, 0xE0, 0xF8, 0xFA, 0xEE, 0xE1, 0xF9, 0xF0),
		// ا ل ي م و ن ر ت ب ة ه د
		mkCandidate("windows-1256", 0xC7, 0xE1, 0xED, 0xE3, 0xE6, 0xE4, 0xD1, 0xCA, 0xC8, 0xC9, 0xE5, 0xCF),
		// า น ร อ ม ห ย ง ว ะ เ แ ก
		mkCandidate("tis-620", 0xD2, 0xB9, 0xC3, 0xCD, 0xC1, 0xCB, 0xC2, 0xA7, 0xC7, 0xD0, 0xE0, 0xE1, 0xA1),
	}
}()

// detectScript picks the single-byte encoding of a non-Latin script whose
// commonest letters make up most of the high bytes; "" when none does.
func detectScript(data []byte, high int) string {
	best, bestN := "", 0
	for _, c := range scriptCandidates {
		n := 0
		for _, b := range data {
			if b >= 0x80 && c.common[b] {
				n++
			}
		}
		if n > bestN {
			best, bestN = c.name, n
		}
	}
	if high >= 2 && bestN*100 >= high*45 {
		return best
	}
	return ""
}

// detectLatin separates the Latin code pages by the bytes they use inside
// words: bytes 0x80-0x9F are letters or punctuation of the Windows pages, a
// symbol position of ISO-8859-1 used between two letters is a letter of
// ISO-8859-2, and the Turkish dotted/dotless i and the cedilla letters stand
// where ISO-8859-1 has rare Icelandic ones.
func detectLatin(data []byte) string {
	isLetter := func(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= 0xC0 }
	c1, c1Punct, inWord, turk := 0, 0, 0, 0
	for i, b := range data {
		switch {
		case b >= 0x80 && b <= 0x9F:
			c1++
			switch b {
			case 0x80, 0x85, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x99:
				c1Punct++
			}
		case b >= 0xA1 && b <= 0xBF && i > 0 && i+1 < len(data) && isLetter(data[i-1]) && isLetter(data[i+1]):
			switch b {
			case 0xB5, 0xAA, 0xBA: // µ ª º also appear in Western text
			default:
				inWord++
			}
		case b == 0xD0 || b == 0xDD || b == 0xDE || b == 0xF0 || b == 0xFD || b == 0xFE:
			if i > 0 && i+1 < len(data) && isLetter(data[i-1]) && isLetter(data[i+1]) {
				turk++
			}
		}
	}
	switch {
	case c1 > 0 && c1Punct == c1:
		return "windows-1252"
	case c1 > 0:
		return "windows-1250"
	case turk >= 2:
		return "iso-8859-9"
	case inWord >= 2:
		return "iso-8859-2"
	}
	return "iso-8859-1"
}

// highRuns reports whether most high bytes sit next to another one: text of a
// non-Latin script is made of them, Latin text has isolated accents.
func highRuns(data []byte, high int) bool {
	n := 0
	for i, b := range data {
		if b >= 0x80 && (i > 0 && data[i-1] >= 0x80 || i+1 < len(data) && data[i+1] >= 0x80) {
			n++
		}
	}
	return n*100 >= high*75
}
