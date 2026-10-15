// Package subtitle reads and writes text subtitle tracks and resolves the
// names of external subtitle files.
//
// Parsers exist for SubRip, WebVTT, (Advanced) SubStation Alpha and SAMI;
// writers for SubRip, WebVTT, ASS, SSA, TTML and the JSON event list the
// web player consumes. Cue times are 100 ns ticks, the unit of the wire.
package subtitle

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the markup family of a parsed track; it decides how a cue's text
// is rewritten for another output format.
type Kind int

const (
	KindSRT Kind = iota
	KindVTT
	KindASS // an ".ass" file: override tags are dropped for HTML-like targets
	KindSSA // an ".ssa" file: override tags become HTML-like tags
	KindSAMI
)

// Cue is one timed text.
type Cue struct {
	Start, End int64 // ticks
	Text       string
}

// Track is a parsed subtitle file.
type Track struct {
	Kind Kind
	Cues []Cue
	// Style is the first style of an ASS/SSA file (nil for other kinds).
	Style *Style
}

// Style is the part of an ASS style the TTML and SSA writers carry over.
type Style struct {
	Name, Font      string
	Size            string
	Primary         string // "&H00BBGGRR" as written in the file
	Bold, Italic    string
	rest            []string // Underline .. Encoding fields, verbatim
	BorderStyle     string
	Outline, Shadow string
	Align           string
	ML, MR, MV      string
	Encoding        string
}

const ticksPerMs = 10_000

// Parse reads data (SubRip, WebVTT, ASS/SSA or SAMI text) as a track of the
// given kind. Unparseable input yields an empty track, not an error.
func Parse(kind Kind, data []byte) Track {
	text := string(data)
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	t := Track{Kind: kind}
	switch kind {
	case KindSRT:
		t.Cues = parseSRT(text)
	case KindVTT:
		t.Cues = parseVTT(text)
	case KindASS, KindSSA:
		t.Cues, t.Style = parseASS(text)
	case KindSAMI:
		t.Cues = parseSAMI(text)
	}
	return t
}

var timeRe = regexp.MustCompile(`^\s*(?:(\d+):)?(\d{1,2}):(\d{2})[,.](\d{1,3})\s*-->\s*(?:(\d+):)?(\d{1,2}):(\d{2})[,.](\d{1,3})`)

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// frac scales a 1-3 digit decimal fraction to milliseconds.
func frac(s string) int64 {
	for len(s) < 3 {
		s += "0"
	}
	return atoi(s[:3])
}

func parseTimingLine(line string) (start, end int64, ok bool) {
	m := timeRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	s := ((atoi(m[1])*60+atoi(m[2]))*60+atoi(m[3]))*1000 + frac(m[4])
	e := ((atoi(m[5])*60+atoi(m[6]))*60+atoi(m[7]))*1000 + frac(m[8])
	return s * ticksPerMs, e * ticksPerMs, true
}

func parseSRT(text string) []Cue {
	var cues []Cue
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var cur *Cue
	var lines []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.Join(lines, "\n")
			cues = append(cues, *cur)
		}
		cur, lines = nil, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if s, e, ok := parseTimingLine(line); ok {
			flush()
			cur = &Cue{Start: s, End: e}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		lines = append(lines, line)
	}
	flush()
	// A trailing cue index line ("12") that precedes the next timing line is
	// not text: it was appended to the previous cue before the blank-line
	// split could see it only when the blank line is missing.
	for i := range cues {
		cues[i].Text = strings.TrimRight(cues[i].Text, "\n")
	}
	return cues
}

func parseVTT(text string) []Cue {
	var cues []Cue
	blocks := strings.Split(text, "\n\n")
	for _, b := range blocks {
		lines := strings.Split(strings.Trim(b, "\n"), "\n")
		for i, l := range lines {
			if s, e, ok := parseTimingLine(l); ok {
				cues = append(cues, Cue{Start: s, End: e, Text: strings.Join(lines[i+1:], "\n")})
				break
			}
		}
	}
	return cues
}

func parseASSTime(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	sec := strings.SplitN(parts[2], ".", 2)
	h, m, sc := atoi(parts[0]), atoi(parts[1]), atoi(sec[0])
	var cs int64
	if len(sec) == 2 {
		cs = frac(sec[1])
	}
	return (((h*60+m)*60+sc)*1000 + cs) * ticksPerMs, true
}

func parseASS(text string) ([]Cue, *Style) {
	var cues []Cue
	var style *Style
	var styleFormat []string
	inEvents, inStyles := false, false
	startIdx, endIdx, textIdx, nfields := 1, 2, 9, 10
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			inEvents = strings.EqualFold(l, "[Events]")
			inStyles = strings.EqualFold(l, "[V4+ Styles]") || strings.EqualFold(l, "[V4 Styles]")
			continue
		}
		low := strings.ToLower(l)
		if !inEvents {
			if strings.HasPrefix(low, "format:") && styleFormat == nil && inStyles {
				for _, f := range strings.Split(l[len("format:"):], ",") {
					styleFormat = append(styleFormat, strings.ToLower(strings.TrimSpace(f)))
				}
			} else if strings.HasPrefix(low, "style:") && style == nil && inStyles && styleFormat != nil {
				vals := strings.Split(strings.TrimSpace(l[len("style:"):]), ",")
				get := func(k string) string {
					for i, f := range styleFormat {
						if f == k && i < len(vals) {
							return strings.TrimSpace(vals[i])
						}
					}
					return ""
				}
				style = &Style{Name: get("name"), Font: get("fontname"), Size: get("fontsize"), Primary: get("primarycolour"),
					Bold: get("bold"), Italic: get("italic"), BorderStyle: get("borderstyle"), Outline: get("outline"),
					Shadow: get("shadow"), Align: get("alignment"), ML: get("marginl"), MR: get("marginr"), MV: get("marginv"),
					Encoding: get("encoding")}
			}
			continue
		}
		switch {
		case strings.HasPrefix(low, "format:"):
			fields := strings.Split(l[len("format:"):], ",")
			nfields = len(fields)
			for i, f := range fields {
				switch strings.ToLower(strings.TrimSpace(f)) {
				case "start":
					startIdx = i
				case "end":
					endIdx = i
				case "text":
					textIdx = i
				}
			}
		case strings.HasPrefix(low, "dialogue:"):
			parts := strings.SplitN(l[len("dialogue:"):], ",", nfields)
			if len(parts) < nfields || textIdx >= len(parts) {
				continue
			}
			s, ok1 := parseASSTime(parts[startIdx])
			e, ok2 := parseASSTime(parts[endIdx])
			if !ok1 || !ok2 {
				continue
			}
			cues = append(cues, Cue{Start: s, End: e, Text: strings.TrimSpace(parts[textIdx])})
		}
	}
	return cues, style
}

var (
	samiSyncRe = regexp.MustCompile(`(?is)<sync\s+start\s*=\s*["']?(\d+)["']?[^>]*>`)
	samiTagRe  = regexp.MustCompile(`(?is)<[^>]*>`)
	samiBrRe   = regexp.MustCompile(`(?is)<br\s*/?>`)
)

func parseSAMI(text string) []Cue {
	idx := samiSyncRe.FindAllStringSubmatchIndex(text, -1)
	var cues []Cue
	for i, m := range idx {
		start := atoi(text[m[2]:m[3]]) * ticksPerMs
		bodyEnd := len(text)
		if i+1 < len(idx) {
			bodyEnd = idx[i+1][0]
		}
		body := text[m[1]:bodyEnd]
		body = samiBrRe.ReplaceAllString(body, "\n")
		body = samiTagRe.ReplaceAllString(body, "")
		body = strings.ReplaceAll(body, "&nbsp;", " ")
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		end := start + 10_000_000
		if i+1 < len(idx) {
			end = atoi(text[idx[i+1][2]:idx[i+1][3]]) * ticksPerMs
		}
		cues = append(cues, Cue{Start: start, End: end, Text: body})
	}
	return cues
}

// Window returns the cues that remain after the offsets of one stream
// request: when copyTimestamps is false every time is moved back by
// startTicks (a negative value moves them forward) and a cue that then ends at or before zero is dropped (a start
// before zero is clamped to zero); a cue that starts after endTicks
// (the original timeline, endTicks > 0) is dropped.
func (t Track) Window(startTicks, endTicks int64, copyTimestamps bool) Track {
	out := Track{Kind: t.Kind, Style: t.Style}
	for _, c := range t.Cues {
		if endTicks > 0 && c.Start > endTicks {
			continue
		}
		if !copyTimestamps && startTicks != 0 {
			c.Start -= startTicks
			c.End -= startTicks
			if c.End <= 0 {
				continue
			}
			if c.Start < 0 {
				c.Start = 0
			}
		}
		out.Cues = append(out.Cues, c)
	}
	return out
}

// Format identifies an output format.
type Format string

const (
	FormatSRT  Format = "srt"
	FormatVTT  Format = "vtt"
	FormatASS  Format = "ass"
	FormatSSA  Format = "ssa"
	FormatJSON Format = "json"
	FormatTTML Format = "ttml"
)

// ParseFormat maps a route extension (any case) to an output Format. The
// long names "subrip" and "webvtt" name the same writers; alias is true for
// them (the reference serves those as application/octet-stream and never
// passes a file through).
func ParseFormat(ext string) (f Format, alias, ok bool) {
	switch strings.ToLower(ext) {
	case "subrip":
		return FormatSRT, true, true
	case "webvtt":
		return FormatVTT, true, true
	}
	f, ok = parseFormat(ext)
	return f, false, ok
}

func parseFormat(ext string) (Format, bool) {
	switch strings.ToLower(ext) {
	case "srt":
		return FormatSRT, true
	case "vtt":
		return FormatVTT, true
	case "ass":
		return FormatASS, true
	case "ssa":
		return FormatSSA, true
	case "json":
		return FormatJSON, true
	case "ttml":
		return FormatTTML, true
	}
	return "", false
}

// ContentType is the media type a format is served with.
func (f Format) ContentType() string {
	switch f {
	case FormatSRT:
		return "application/x-subrip"
	case FormatVTT:
		return "text/vtt"
	case FormatASS, FormatSSA:
		return "text/x-ssa"
	case FormatJSON:
		return "application/json"
	case FormatTTML:
		return "application/ttml+xml"
	}
	return "application/octet-stream"
}

var (
	assTagRe  = regexp.MustCompile(`\{[^}]*\}`)
	htmlTagRe = regexp.MustCompile(`(?i)</?([a-z]+)(?:\s[^>]*)?>`)
)

// assToHTML turns the italic/bold/underline overrides of an ASS line into
// HTML-like tags and drops every other override; "\N" and "\n" become line
// breaks.
func assToHTML(s string) string {
	s = assTagRe.ReplaceAllStringFunc(s, func(m string) string {
		var b strings.Builder
		for _, t := range []struct{ on, off, tag string }{
			{`\i1`, `\i0`, "i"}, {`\b1`, `\b0`, "b"}, {`\u1`, `\u0`, "u"},
		} {
			if strings.Contains(m, t.on) {
				b.WriteString("<" + t.tag + ">")
			}
			if strings.Contains(m, t.off) {
				b.WriteString("</" + t.tag + ">")
			}
		}
		return b.String()
	})
	return assBreaks(s)
}

func assBreaks(s string) string {
	s = strings.ReplaceAll(s, `\N`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	return strings.ReplaceAll(s, `\h`, " ")
}

func stripASS(s string) string {
	return assBreaks(assTagRe.ReplaceAllString(s, ""))
}

// htmlToASS turns <i>, <b> and <u> into override tags, drops the other tags
// and writes line breaks as "\N".
func htmlToASS(s string) string {
	s = htmlTagRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlTagRe.FindStringSubmatch(m)
		name := strings.ToLower(sub[1])
		closing := strings.HasPrefix(m, "</")
		code := map[string]string{"i": "i", "b": "b", "u": "u"}[name]
		if code == "" {
			return ""
		}
		if closing {
			return `{\` + code + `0}`
		}
		return `{\` + code + `1}`
	})
	return strings.ReplaceAll(s, "\n", `\N`)
}

func htmlToTTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = htmlTagRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlTagRe.FindStringSubmatch(m)
		name := strings.ToLower(sub[1])
		if strings.HasPrefix(m, "</") {
			if name == "i" || name == "b" || name == "u" {
				return "</span>"
			}
			return ""
		}
		switch name {
		case "i":
			return `<span tts:fontStyle="italic">`
		case "b":
			return `<span tts:fontWeight="bold">`
		case "u":
			return `<span tts:textDecoration="underline">`
		}
		return ""
	})
	return strings.ReplaceAll(s, "\n", "<br />")
}

// text returns c's text as HTML-like markup with "\n" line breaks, the
// working form of every writer except ASS/SSA and JSON.
func (t Track) markup(c Cue) string {
	switch t.Kind {
	case KindASS:
		return stripASS(c.Text)
	case KindSSA:
		return assToHTML(c.Text)
	}
	return c.Text
}

// raw is the text for the formats that keep a track's own markup (SubRip
// and the JSON list): override tags of an ".ass" file stay as they are.
func (t Track) raw(c Cue) string {
	switch t.Kind {
	case KindASS:
		return assBreaks(c.Text)
	case KindSSA:
		return assToHTML(c.Text)
	}
	return c.Text
}

func ticksToParts(ticks int64) (h, m, s, ms int64) {
	if ticks < 0 {
		ticks = 0
	}
	total := ticks / ticksPerMs
	ms = total % 1000
	total /= 1000
	s = total % 60
	total /= 60
	m = total % 60
	h = total / 60
	return
}

func fmtClock(ticks int64, sep byte) string {
	h, m, s, ms := ticksToParts(ticks)
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", h, m, s, sep, ms)
}

func fmtASS(ticks int64) string {
	h, m, s, ms := ticksToParts(ticks)
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, ms/10)
}

func fmtSeconds(ticks int64) string {
	ms := ticks / ticksPerMs
	whole, f := ms/1000, ms%1000
	if f == 0 {
		return fmt.Sprintf("%d.0", whole)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%03d", whole, f), "0")
}

// Options tune Write.
type Options struct {
	// VTTTimeMap adds the X-TIMESTAMP-MAP header to WebVTT output.
	VTTTimeMap bool
}

// Write renders t in format f.
func (t Track) Write(f Format, o Options) []byte {
	var b strings.Builder
	switch f {
	case FormatSRT:
		for i, c := range t.Cues {
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, fmtClock(c.Start, ','), fmtClock(c.End, ','), t.raw(c))
		}
	case FormatVTT:
		b.WriteString("WEBVTT\n")
		if o.VTTTimeMap {
			b.WriteString("X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n")
		}
		b.WriteString("\n")
		for i, c := range t.Cues {
			if i > 0 {
				b.WriteString("\n\n")
			}
			text := strings.ReplaceAll(t.markup(c), "&", "&amp;")
			fmt.Fprintf(&b, "%s --> %s\n%s", fmtClock(c.Start, '.'), fmtClock(c.End, '.'), text)
		}
	case FormatASS, FormatSSA:
		if f == FormatASS {
			if t.Kind == KindVTT {
				b.WriteString(assHeaderLF)
			} else {
				b.WriteString(assHeader)
			}
		} else if st := t.Style; st != nil && (t.Kind == KindASS || t.Kind == KindSSA) {
			b.WriteString(strings.Replace(ssaHeader, ssaDefaultStyle, st.ssaLine()+"\n\r\n", 1))
		} else {
			b.WriteString(ssaHeader)
		}
		for _, c := range t.Cues {
			text := c.Text
			if t.Kind == KindSRT || t.Kind == KindVTT || t.Kind == KindSAMI {
				text = htmlToASS(c.Text)
			}
			if f == FormatASS {
				fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", fmtASS(c.Start), fmtASS(c.End), text)
			} else {
				fmt.Fprintf(&b, "Dialogue: Marked=0,%s,%s,*Default,NTP,0000,0000,0000,,%s\n", fmtASS(c.Start), fmtASS(c.End), text)
			}
		}
	case FormatJSON:
		b.WriteString(`{"TrackEvents":[`)
		for i, c := range t.Cues {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"Id":"%d","Text":%s,"StartPositionTicks":%d,"EndPositionTicks":%d}`,
				i+1, jsonString(t.raw(c)), c.Start, c.End)
		}
		b.WriteString(`]}`)
	case FormatTTML:
		head := ttmlHeader
		if st := t.Style; st != nil && (t.Kind == KindASS || t.Kind == KindSSA) {
			head = strings.Replace(head, ttmlDefaultStyle, st.ttmlLine(), 1)
		}
		b.WriteString(head)
		for i, c := range t.Cues {
			if i > 0 {
				b.WriteString("\n      ")
			}
			extra := ""
			if t.Kind == KindASS || t.Kind == KindSSA {
				extra = ` style="Default" xmlns="http://www.w3.org/ns/ttml"`
			}
			fmt.Fprintf(&b, `<p begin="%ss" xml:id="p%d" end="%ss"%s>%s</p>`,
				fmtSeconds(c.Start), i, fmtSeconds(c.End), extra, htmlToTTML(t.markup(c)))
		}
		b.WriteString("\n    ")
		b.WriteString(ttmlFooter)
	}
	return []byte(b.String())
}

// jsonString encodes s like the reference's JSON writer: ASCII letters and
// digits are literal, the characters an HTML-safe encoder rewrites and
// everything outside ASCII become upper-case \uXXXX escapes.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20, r >= 0x7f, strings.ContainsRune(`"&'<>+`+"`", r):
			if r > 0xFFFF {
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04X\u%04X`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			} else {
				fmt.Fprintf(&b, `\u%04X`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// InsertVTTTimeMap adds the X-TIMESTAMP-MAP header to a WebVTT document that
// is served as it is (the header goes right after the signature line).
func InsertVTTTimeMap(doc []byte) []byte {
	s := string(doc)
	i := strings.Index(s, "\n")
	if i < 0 || !strings.HasPrefix(strings.TrimPrefix(s, "\ufeff"), "WEBVTT") {
		return doc
	}
	return []byte(s[:i+1] + "X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n" + s[i+1:])
}

const (
	ssaDefaultStyle  = "Style: Default,Arial,20,16777215,65535,0,0,0,0,1,1,1,2,10,10,10,0,1\r\n"
	ttmlDefaultStyle = `<style xml:id="s0" tts:backgroundColor="black" tts:fontStyle="normal" tts:fontSize="16px" tts:fontFamily="sansSerif" tts:color="white" />`
)

// assColor converts "&HAABBGGRR" (or "&HBBGGRR") to red, green, blue.
func assColor(s string) (r, g, b int64) {
	s = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "&H")
	v, err := strconv.ParseInt(strings.TrimSuffix(s, "&"), 16, 64)
	if err != nil {
		return 255, 255, 255
	}
	return v & 0xFF, (v >> 8) & 0xFF, (v >> 16) & 0xFF
}

func (st *Style) ssaLine() string {
	r, g, b := assColor(st.Primary)
	c := func(r, g, b int64) int64 { return int64(int32(uint32(0xFF000000) | uint32(r<<16|g<<8|b))) }
	return fmt.Sprintf("Style: %s,%s,%s,%d,%d,%d,%d,%s,%s,%s,%s,%s,%s,%s,%s,%s,0,%s",
		st.Name, st.Font, st.Size, c(r, g, b), int64(-65536), int64(-16777216), int64(-16777216),
		st.Bold, st.Italic, st.BorderStyle, st.Outline, st.Shadow, st.Align, st.ML, st.MR, st.MV, st.Encoding)
}

func (st *Style) ttmlLine() string {
	r, g, b := assColor(st.Primary)
	weight, style := "normal", "normal"
	if st.Bold != "" && st.Bold != "0" {
		weight = "bold"
	}
	if st.Italic != "" && st.Italic != "0" {
		style = "italic"
	}
	return fmt.Sprintf(`<style xml:id="%s" tts:fontFamily="%s" tts:fontWeight="%s" tts:fontStyle="%s" tts:color="#%02x%02x%02x" tts:fontSize="%s" />`,
		st.Name, st.Font, weight, style, r, g, b, st.Size)
}
