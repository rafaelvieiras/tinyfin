package transcode

import (
	"bytes"
	"unicode/utf8"
)

// detectCharset names the text encoding of a subtitle file the way the
// "charenc" option of the subtitles filter wants it, or "" when none should be
// given (an encoding marked by a byte order mark other than UTF-8's is read by
// the filter itself). It is a heuristic over the bytes: ASCII, UTF-8, Shift-JIS,
// GB18030, windows-1251 and ISO-8859-1 are told apart, which covers what
// subtitle files are written in nearly always.
func detectCharset(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return "utf-8"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}),
		bytes.HasPrefix(data, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return ""
	}
	high, letters, upperTail := 0, 0, 0
	for _, b := range data {
		switch {
		case b >= 0x80:
			high++
			if b >= 0xE0 {
				upperTail++
			}
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z':
			letters++
		}
	}
	if high == 0 {
		return "ascii"
	}
	if utf8.Valid(data) {
		return "utf-8"
	}
	if validDoubleByte(data, 0x81, 0x9F, 0xE0, 0xFC, 0x40, 0xFC) && hasKana(data) {
		return "shift-jis"
	}
	// Cyrillic text is mostly high bytes, nearly all of them lowercase letters;
	// accented Latin text is mostly ASCII
	if high*10 >= (high+letters)*4 && upperTail*10 >= high*7 {
		return "windows-1251"
	}
	if validDoubleByte(data, 0x81, 0xFE, 0, 0, 0x40, 0xFE) {
		return "gb18030"
	}
	return "iso-8859-1"
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
