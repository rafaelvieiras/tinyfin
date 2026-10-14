package storage

import (
	_ "embed"
	"encoding/binary"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Transliteration for sort names (contract: items.sort.R8).
//
// The tables are data observed from the reference server, one code point at
// a time (see docs/contract/specs/items.md); there is no third-party
// dependency and nothing is derived from another project's tables.
//
//   - translit_table.txt: "HEX<TAB>value" for every probed code point whose
//     sort-name form differs from its simple lower-case form (decimal digits
//     are stored as the single digit they become);
//   - translit_plain.txt: the non-ASCII code points that do not switch on the
//     "non-Latin string" behaviour (R8: ASCII punctuation is dropped);
//   - translit_han.bin: Han readings (unaccented pinyin) for U+3400-4DBF,
//     U+4E00-9FFF and U+F900-FAFF as a syllable list plus one 16-bit index
//     per code point (0 = unchanged).
//
// Hangul syllables are composed from the initial / vowel / final tables
// below, which match every observed syllable.

//go:embed translit_table.txt
var translitTableSrc string

//go:embed translit_plain.txt
var translitPlainSrc string

//go:embed translit_han.bin
var translitHanBin []byte

var hanRanges = [...][2]rune{{0x3400, 0x4DBF}, {0x4E00, 0x9FFF}, {0xF900, 0xFAFF}}

type translitTables struct {
	keys  []rune
	vals  []string
	plain []rune
	syl   []string
	han   []byte // 2 bytes per code point, ranges concatenated
}

var (
	translitOnce sync.Once
	translitTab  translitTables
)

func loadTranslit() *translitTables {
	translitOnce.Do(func() {
		t := &translitTab
		for _, line := range strings.Split(translitTableSrc, "\n") {
			hex, val, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			cp, err := strconv.ParseUint(hex, 16, 32)
			if err != nil {
				continue
			}
			t.keys = append(t.keys, rune(cp))
			t.vals = append(t.vals, val)
		}
		for _, line := range strings.Split(translitPlainSrc, "\n") {
			if cp, err := strconv.ParseUint(line, 16, 32); err == nil {
				t.plain = append(t.plain, rune(cp))
			}
		}
		b := translitHanBin
		n := int(binary.LittleEndian.Uint16(b))
		b = b[2:]
		for i := 0; i < n; i++ {
			l := int(b[0])
			t.syl = append(t.syl, string(b[1:1+l]))
			b = b[1+l:]
		}
		t.han = b
	})
	return &translitTab
}

func (t *translitTables) lookup(r rune) (string, bool) {
	i := sort.Search(len(t.keys), func(i int) bool { return t.keys[i] >= r })
	if i < len(t.keys) && t.keys[i] == r {
		return t.vals[i], true
	}
	return "", false
}

func (t *translitTables) isPlain(r rune) bool {
	i := sort.Search(len(t.plain), func(i int) bool { return t.plain[i] >= r })
	return i < len(t.plain) && t.plain[i] == r
}

// hanReading returns the reading of a Han ideograph and whether r is one
// with a known reading.
func (t *translitTables) hanReading(r rune) (string, bool) {
	off := rune(0)
	for _, rg := range hanRanges {
		if r >= rg[0] && r <= rg[1] {
			p := int(off+r-rg[0]) * 2
			if p+2 > len(t.han) {
				return "", false
			}
			idx := int(binary.LittleEndian.Uint16(t.han[p:]))
			if idx == 0 || idx > len(t.syl) {
				return "", false
			}
			return t.syl[idx-1], true
		}
		off += rg[1] - rg[0] + 1
	}
	return "", false
}

var (
	hangulInitial = [...]string{"g", "kk", "n", "d", "tt", "l", "m", "b", "pp", "s", "ss", "", "j", "jj", "ch", "k", "t", "p", "h"}
	hangulVowel   = [...]string{"a", "ae", "ya", "yae", "eo", "e", "yeo", "ye", "o", "wa", "wae", "oe", "yo", "u", "wo", "we", "wi", "yu", "eu", "ui", "i"}
	hangulFinal   = [...]string{"", "g", "kk", "gs", "n", "nj", "nh", "d", "l", "lg", "lm", "lb", "ls", "lt", "lp", "lh", "m", "b", "bs", "s", "ss", "ng", "j", "ch", "k", "t", "p", "h"}
)

func hangulSyllable(r rune) (string, bool) {
	if r < 0xAC00 || r > 0xD7A3 {
		return "", false
	}
	s := int(r - 0xAC00)
	return hangulInitial[s/588] + hangulVowel[s%588/28] + hangulFinal[s%28], true
}

// nextIsDigit reports whether the code point after position i is a decimal
// digit of the BMP (the digit runs are already zero-padded here).
func nextIsDigit(in []rune, i int) bool {
	if i+1 >= len(in) {
		return false
	}
	r := in[i+1]
	return r <= 0xFFFF && unicode.IsDigit(r)
}

// halfwidthVoiced maps the Latin reading of a halfwidth katakana letter to
// the reading it gets when a voiced (U+FF9E) or semi-voiced (U+FF9F) sound
// mark follows.
var halfwidthVoiced = map[string]string{
	"ka": "ga", "ki": "gi", "ku": "gu", "ke": "ge", "ko": "go",
	"sa": "za", "shi": "ji", "su": "zu", "se": "ze", "so": "zo",
	"ta": "da", "chi": "dji", "tsu": "dzu", "te": "de", "to": "do",
	"ha": "ba", "hi": "bi", "fu": "bu", "he": "be", "ho": "bo", "u": "vu",
}

var halfwidthSemiVoiced = map[string]string{
	"ha": "pa", "hi": "pi", "fu": "pu", "he": "pe", "ho": "po",
}

// isKana reports whether r is a hiragana, katakana or halfwidth katakana
// character (the prolonged sound mark included).
func isKana(r rune) bool {
	return (r >= 0x3041 && r <= 0x30FF) || (r >= 0xFF66 && r <= 0xFF9F)
}

func isVirama(r rune) bool {
	switch r {
	case 0x094D, 0x09CD, 0x0A4D, 0x0ACD, 0x0B4D, 0x0BCD, 0x0C4D, 0x0CCD, 0x0D4D, 0x0DCA:
		return true
	}
	return false
}

// Character classes of the previous significant character, for the
// spacing around Han readings.
const (
	prevStart  = iota // nothing yet
	prevLetter        // a letter (any script), or a closing guillemet
	prevSpace         // white space
	prevDigit         // a decimal digit
	prevOther         // symbol, or a dropped punctuation mark that blocks a space
)

// transliterate lower-cases and transliterates a (already de-punctuated and
// digit-padded) string into the form sort names use.
func transliterate(in []rune) string {
	t := loadTranslit()
	foreign := false
	for _, r := range in {
		if r >= 0x80 && !t.isPlain(r) {
			foreign = true
			break
		}
	}
	var b strings.Builder
	b.Grow(len(in) + 8)
	prev := prevStart
	prevHan := false
	prevThai := false
	sep := false // a dropped separator directly precedes
	gap := false // a dropped character sits between the previous letter and here
	// consonantEnd is the byte offset in b where the last Indic consonant
	// ended (its inherent "a" is the final byte); -1 when the previous
	// character was not one.
	consonantEnd := -1
	var consonantBlock rune
	thaiOrphan := false // the Thai run began with a vowel mark
	// persian: the previous character is an extended Arabic-Indic digit,
	// which sets Thai runs and viramas apart like an Arabic letter does.
	persian := false
	lastASCIILetter := false // the previous character was a-z / A-Z
	greekVowel := false      // the previous character was a Greek a, e or o
	reset := func(class int) {
		prev, prevHan, prevThai, sep, gap, consonantEnd = class, false, false, false, false, -1
		thaiOrphan, persian, lastASCIILetter, greekVowel = false, false, false, false
	}
	for i, r := range in {
		if r < 0x80 {
			if foreign {
				switch r {
				case '!', ')', ':', ';', '?', ']':
					sep = true
					prev, prevHan, prevThai, consonantEnd = prevOther, false, false, -1
					continue
				case '(', '[':
					if prevHan || (lastASCIILetter && nextIsDigit(in, i)) {
						b.WriteByte(' ')
						reset(prevSpace)
					}
					gap = true
					continue
				case '"', '#', '*', '/', '@', '\\', '_':
					reset(prevOther)
					continue
				}
			}
			switch {
			case r >= 'A' && r <= 'Z':
				b.WriteByte(byte(r + 32))
				reset(prevLetter)
				lastASCIILetter = true
			case r >= 'a' && r <= 'z':
				b.WriteByte(byte(r))
				reset(prevLetter)
				lastASCIILetter = true
			case r == ' ' || r == '\t':
				b.WriteByte(byte(r))
				reset(prevSpace)
			case r >= '0' && r <= '9':
				b.WriteByte(byte(r))
				reset(prevDigit)
			default:
				b.WriteByte(byte(r))
				reset(prevOther)
			}
			continue
		}
		if foreign && unicode.Is(unicode.Zs, r) {
			b.WriteByte(' ')
			reset(prevSpace)
			continue
		}
		if foreign {
			switch r {
			case '、', '」', '』', '】', '，', '！', '？', '：', '；', '）', '”', '’':
				sep = true
				prev, prevHan, prevThai, consonantEnd = prevOther, false, false, -1
				continue
			case '「', '『', '【', '（', '“', '‘', '„', '‚':
				if prevHan || (lastASCIILetter && nextIsDigit(in, i)) {
					b.WriteByte(' ')
					reset(prevSpace)
				}
				gap = true
				continue
			case '。', '·', '・', '—', '–', '…':
				reset(prevOther)
				continue
			case '«', '《':
				if prev == prevLetter && b.Len() > 0 {
					next := false
					if i+1 < len(in) {
						_, next = t.hanReading(in[i+1])
					}
					if prevHan || next {
						b.WriteByte(' ')
					}
				}
				b.WriteString("<<")
				reset(prevOther)
				continue
			case '»', '》':
				b.WriteString(">>")
				reset(prevOther)
				sep = true
				continue
			}
		}
		if rd, ok := t.hanReading(r); ok {
			if prev == prevLetter || sep {
				b.WriteByte(' ')
			}
			b.WriteString(rd)
			reset(prevLetter)
			prevHan = true
			continue
		}
		if r == 0x130 && foreign {
			b.WriteByte('i')
			reset(prevLetter)
			continue
		}
		if (r == 'γ' || r == 'Γ') && i+1 < len(in) {
			switch in[i+1] {
			case 'γ', 'Γ', 'κ', 'Κ', 'ξ', 'Ξ', 'χ', 'Χ':
				// gamma before a velar is the nasal "n"
				b.WriteByte('n')
				reset(prevLetter)
				continue
			}
		}
		if (r == 0x30FC || r == 0xFF70) && ((i > 0 && isKana(in[i-1])) || (i+1 < len(in) && isKana(in[i+1]))) {
			// the prolonged sound mark next to kana only lengthens a vowel
			continue
		}
		isDigit := r <= 0xFFFF && unicode.IsDigit(r)
		isThai := r >= 0x0E00 && r <= 0x0E7F && !isDigit
		if isThai {
			// A Thai run that follows another script is set off by a
			// space: marks after letters and digits, consonants after
			// letters, and a consonant after a run that began with a mark.
			switch {
			case unicode.IsMark(r) && (prev == prevLetter || persian) && !prevThai && !gap:
				b.WriteByte(' ')
			case unicode.IsLetter(r) && (prev == prevLetter || persian) && !prevThai && !gap:
				b.WriteByte(' ')
			case unicode.IsLetter(r) && thaiOrphan:
				b.WriteByte(' ')
			}
			if unicode.IsMark(r) && !prevThai {
				thaiOrphan = true
			} else if unicode.IsLetter(r) {
				thaiOrphan = false
			}
		}
		var out string
		if v, ok := t.lookup(r); ok {
			out = v
			if r >= 0xFF66 && r <= 0xFF9D && i+1 < len(in) {
				switch in[i+1] {
				case 0xFF9E:
					if w, ok := halfwidthVoiced[v]; ok {
						out = w
					}
				case 0xFF9F:
					if w, ok := halfwidthSemiVoiced[v]; ok {
						out = w
					}
				}
			}
		} else if v, ok := hangulSyllable(r); ok {
			out = v
		} else {
			out = string(unicode.ToLower(r))
		}
		greek := (r >= 0x0370 && r <= 0x03FF) || (r >= 0x1F00 && r <= 0x1FFF)
		if greek && greekVowel && out == "y" {
			out = "u" // upsilon after a vowel is "u" (au, eu, ou)
		}
		if foreign && out == "" && unicode.IsPunct(r) {
			sep = true
			prev, prevHan, prevThai, consonantEnd = prevOther, false, false, -1
			continue
		}
		markerEmitted := false
		if r >= 0x0900 && r <= 0x0DFF && unicode.IsMark(r) {
			sameBlock := consonantEnd >= 0 && consonantEnd == b.Len() && consonantBlock == r>>7
			switch {
			case sameBlock && r == 0x09BE:
				out = "" // the Bengali aa sign adds nothing to a consonant
			case sameBlock && (isVirama(r) || r <= 0x097F || (r >= 0x0B80 && r <= 0x0BFF)):
				// A virama, or a Devanagari or Tamil vowel sign, right
				// after a consonant of its own script replaces the
				// consonant's inherent "a".
				cur := b.String()
				b.Reset()
				b.WriteString(cur[:len(cur)-1])
			case isVirama(r) && !gap && b.Len() > 0 && (prev == prevLetter || persian):
				// Any other virama leaves a private marker in the
				// reference's output.
				b.WriteString("\ue04d")
				markerEmitted = true
			}
		}
		b.WriteString(out)
		switch {
		case unicode.IsLetter(r) || (unicode.IsMark(r) && out != ""):
			prev = prevLetter
		case unicode.IsMark(r):
		case isDigit:
			prev = prevDigit
		default:
			prev = prevOther
		}
		if markerEmitted {
			prev = prevOther
		}
		persian = r >= 0x06F0 && r <= 0x06F9
		lastASCIILetter = false
		greekVowel = greek && (out == "a" || out == "e" || out == "o")
		if !isThai {
			thaiOrphan = false
		}
		prevHan, sep, gap = false, false, markerEmitted
		prevThai = isThai
		if r >= 0x0900 && r <= 0x0DFF && unicode.IsLetter(r) && strings.HasSuffix(out, "a") {
			consonantEnd, consonantBlock = b.Len(), r>>7
		} else {
			consonantEnd = -1
		}
	}
	return b.String()
}
