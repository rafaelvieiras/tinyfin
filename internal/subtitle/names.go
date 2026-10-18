package subtitle

import (
	"strings"
	"unicode"

	"github.com/rafaelvieiras/tinyfin/internal/culture"
)

// extCodec maps the extension of an external subtitle file (lower case,
// no dot) to the codec name the reference reports for it.
var extCodec = map[string]string{
	"srt":  "subrip",
	"ass":  "ass",
	"ssa":  "ass",
	"vtt":  "webvtt",
	"smi":  "sami",
	"sami": "sami",
	"sup":  "PGSSUB",
}

// ExternalCodec returns the codec of an external subtitle file extension.
func ExternalCodec(ext string) (string, bool) {
	c, ok := extCodec[strings.ToLower(strings.TrimPrefix(ext, "."))]
	return c, ok
}

// IsTextCodec reports whether a subtitle codec carries text (it can be
// converted and delivered as a text track) rather than bitmaps.
func IsTextCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "pgssub", "hdmv_pgs_subtitle", "pgs", "dvdsub", "dvd_subtitle", "dvbsub", "dvb_subtitle", "xsub", "vobsub":
		return false
	}
	return true
}

// SupportsExternal reports whether a subtitle stream of this codec can be
// fetched on its own (SupportsExternalStream): every track but DVB pictures,
// which the reference leaves inside the container.
func SupportsExternal(codec string) bool {
	switch strings.ToLower(codec) {
	case "dvbsub", "dvb_subtitle":
		return false
	}
	return true
}

// NormalizeCodec maps an ffprobe subtitle codec name to the name the
// reference server reports (bitmap codecs get their upper-case names).
func NormalizeCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "hdmv_pgs_subtitle":
		return "PGSSUB"
	case "dvd_subtitle":
		return "DVDSUB"
	case "dvb_subtitle":
		return "DVBSUB"
	}
	return codec
}

// Sidecar is what the name of an external subtitle file says.
type Sidecar struct {
	Codec    string
	Ext      string // lower case, no dot
	Language string
	Title    string
	Forced   bool
	Default  bool
	HI       bool
}

// ParseSidecar interprets fileName as an external subtitle of the video
// whose name without extension is videoBase: the file must start with that
// name (any case) followed by a dot and end in a subtitle extension. What
// lies between is split on dots: "forced", "default" and "sdh"/"cc"/"hi" are
// flags, the first token that names a language sets the language, whatever
// is left (joined with dots) is the title.
func ParseSidecar(videoBase, fileName string) (Sidecar, bool) {
	dot := strings.LastIndexByte(fileName, '.')
	if dot < 0 {
		return Sidecar{}, false
	}
	ext := strings.ToLower(fileName[dot+1:])
	codec, ok := extCodec[ext]
	if !ok {
		return Sidecar{}, false
	}
	stem := fileName[:dot]
	if len(stem) <= len(videoBase) || !strings.EqualFold(stem[:len(videoBase)], videoBase) || stem[len(videoBase)] != '.' {
		// "Name.srt" is the video's own subtitle: stem equals the base.
		if strings.EqualFold(stem, videoBase) {
			return Sidecar{Codec: codec, Ext: ext}, true
		}
		return Sidecar{}, false
	}
	sc := Sidecar{Codec: codec, Ext: ext}
	var title []string
	for _, tok := range strings.Split(stem[len(videoBase)+1:], ".") {
		if tok == "" {
			continue
		}
		switch strings.ToLower(tok) {
		case "forced":
			sc.Forced = true
			continue
		case "default":
			sc.Default = true
			continue
		case "sdh", "cc", "hi":
			sc.HI = true
			continue
		}
		if sc.Language == "" {
			if lang, ok := ResolveLanguage(tok); ok {
				sc.Language = lang
				continue
			}
		}
		title = append(title, tok)
	}
	sc.Title = strings.Join(title, ".")
	return sc, true
}

var (
	langByKey map[string]*culture.Culture
)

func init() {
	langByKey = map[string]*culture.Culture{}
	for i := range culture.All {
		c := &culture.All[i]
		region := strings.Contains(c.TwoLetterISOLanguageName, "-")
		add := func(k string) {
			k = strings.ToLower(k)
			if k == "" {
				return
			}
			if _, dup := langByKey[k]; !dup {
				langByKey[k] = c
			}
		}
		if region {
			add(c.Name)
			continue
		}
		add(c.Name)
		add(c.DisplayName)
		add(c.TwoLetterISOLanguageName)
		add(c.ThreeLetterISOLanguageName)
		for _, t := range c.ThreeLetterISOLanguageNames {
			add(t)
		}
	}
	langByKey["und"] = &culture.Culture{Name: "und", DisplayName: "Undetermined", ThreeLetterISOLanguageName: "und"}
}

// ResolveLanguage maps a file-name token (a code of two or three letters, a
// region form such as "pt-BR", or an English language name) to the language
// value stored on a stream: the three-letter code, or the lower-cased
// region form.
func ResolveLanguage(tok string) (string, bool) {
	l := strings.ToLower(tok)
	if l == "zh-hans" || l == "zh-hant" {
		return l, true
	}
	c, ok := langByKey[l]
	if !ok {
		return "", false
	}
	if strings.Contains(c.TwoLetterISOLanguageName, "-") {
		return l, true
	}
	return c.ThreeLetterISOLanguageName, true
}

// DisplayName is the English name of a stream language ("eng" is
// "English", "pt-br" is "Portuguese (Brazil)"); an unknown code is
// returned upper-cased.
func DisplayName(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	switch l {
	case "zh-hans":
		return "Chinese (Simplified)"
	case "zh-hant":
		return "Chinese (Traditional)"
	}
	c, ok := langByKey[l]
	if !ok {
		if i := strings.IndexByte(l, '-'); i > 0 {
			if base, ok := langByKey[l[:i]]; ok {
				return firstName(base.DisplayName)
			}
		}
		return strings.ToUpper(lang)
	}
	return firstName(c.DisplayName)
}

func firstName(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// punctOrder is the collation order of ASCII punctuation (before digits and
// letters) the reference's culture-aware sort uses.
const punctOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func weight(r rune) (primary int, tertiary int) {
	switch {
	case unicode.IsLetter(r):
		lo := unicode.ToLower(r)
		t := 0
		if lo != r {
			t = 1
		}
		return 2000 + int(lo), t
	case unicode.IsDigit(r):
		return 1000 + int(r), 0
	}
	if i := strings.IndexRune(punctOrder, r); i >= 0 {
		return 100 + i, 0
	}
	return 500 + int(r), 0
}

// Compare orders two file names the way the reference orders external
// subtitles: letters ignoring case, punctuation before digits before
// letters, lower case before upper case among otherwise equal names.
func Compare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := len(ra)
	if len(rb) < n {
		n = len(rb)
	}
	tert := 0
	for i := 0; i < n; i++ {
		pa, ta := weight(ra[i])
		pb, tb := weight(rb[i])
		if pa != pb {
			if pa < pb {
				return -1
			}
			return 1
		}
		if tert == 0 && ta != tb {
			if ta < tb {
				tert = -1
			} else {
				tert = 1
			}
		}
	}
	switch {
	case len(ra) < len(rb):
		return -1
	case len(ra) > len(rb):
		return 1
	}
	return tert
}
