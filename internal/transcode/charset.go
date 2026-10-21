package transcode

import (
	"path/filepath"
	"sort"
	"strings"
)

// Charset detection: the statistical detector behind the "charenc" option of a
// text burn-in, specified exactly by bitmap.charset.R1-R20 (docs/contract/specs/
// bitmap.md). The tables are in chardata/ (copies of docs/contract/data, kept
// equal by TestCharsetDataMatchesContract). Confidences are single-precision.

const (
	stDetecting = iota
	stFound
	stNotMe
)

const (
	csShortcut float32 = 0.95
	csThresh   float32 = 0.20
	csSureNo   float32 = 0.01
	csSureYes  float32 = 0.99
)

// detectCharset returns the name the detector gives to the bytes of a file
// ("" when it gives none), before the post-processing of bitmap.charset.R6.
func detectCharset(data []byte) string {
	const chunkSize = 1024
	if len(data) == 0 {
		return ""
	}
	const (
		plain = iota
		escape
		high
	)
	state := plain
	var last byte
	var mb, sb *csGroup
	var l1 *csLatin1
	var esc *csEscape
	for off := 0; off < len(data); off += chunkSize {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]
		if off == 0 && len(chunk) >= 2 {
			if n := csBOM(chunk); n != "" {
				return n
			}
		}
		for _, b := range chunk {
			if state == high {
				break
			}
			if b >= 0x80 && b != 0xA0 {
				state = high
				break
			}
			if state == plain && (b == 0x1B || (b == '{' && last == '~')) {
				state = escape
			}
			last = b
		}
		switch state {
		case high:
			if mb == nil {
				esc = nil
				t := csTables()
				mb = newMBGroup(t)
				sb = newSBGroup(t)
				l1 = &csLatin1{lastClass: lcOTH}
			}
			if mb.feed(chunk) == stFound {
				return mb.name()
			}
			if sb.feed(chunk) == stFound {
				return sb.name()
			}
			l1.feed(chunk)
		case escape:
			if esc == nil {
				esc = newEscape(csTables())
			}
			if name := esc.feed(chunk); name != "" {
				return name
			}
		}
	}
	if state != high {
		return "ascii"
	}
	type cand struct {
		name string
		conf float32
	}
	var cands []cand
	for _, c := range []struct {
		name func() string
		conf func() float32
	}{
		{mb.name, mb.confidence}, {sb.name, sb.confidence}, {l1.name, l1.confidence},
	} {
		n := c.name()
		if cf := c.conf(); cf > csThresh {
			cands = append(cands, cand{n, cf})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].conf > cands[j].conf })
	return cands[0].name
}

// charencFor is the value of the "charenc" option for a subtitle file
// (bitmap.charset.R6): the detector's answer, none for a UTF-16 byte order mark
// on a file the filter converts itself.
func charencFor(path string, data []byte) string {
	name := detectCharset(data)
	if name == "utf-16le" || name == "utf-16be" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ass", ".ssa", ".srt":
			return ""
		}
	}
	return name
}

func csBOM(c []byte) string {
	n := len(c)
	switch {
	case c[0] == 0xFE && c[1] == 0xFF:
		if n >= 4 && c[2] == 0 && c[3] == 0 {
			return "X-ISO-10646-UCS-4-3412"
		}
		return "utf-16be"
	case c[0] == 0xFF && c[1] == 0xFE:
		if n >= 4 && c[2] == 0 && c[3] == 0 {
			return "utf-32le"
		}
		return "utf-16le"
	}
	if n >= 3 && c[0] == 0xEF && c[1] == 0xBB && c[2] == 0xBF {
		return "utf-8"
	}
	if n >= 4 {
		switch {
		case c[0] == 0 && c[1] == 0 && c[2] == 0xFE && c[3] == 0xFF:
			return "utf-32be"
		case c[0] == 0 && c[1] == 0 && c[2] == 0xFF && c[3] == 0xFE:
			return "X-ISO-10646-UCS-4-2143"
		case c[0] == 0x2B && c[1] == 0x2F && c[2] == 0x76 &&
			(c[3] == 0x38 || c[3] == 0x39 || c[3] == 0x2B || c[3] == 0x2F):
			return "utf-7"
		case c[0] == 0x84 && c[1] == 0x31 && c[2] == 0x95 && c[3] == 0x33:
			return "gb18030"
		}
	}
	return ""
}

func csIsLetter(b byte) bool { return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') }
