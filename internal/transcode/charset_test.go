package transcode

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDetectCharset(t *testing.T) {
	latin := []byte("Ol\xe1, a\xe7\xe3o n\xfamero tr\xeas")
	sjis := []byte("\x82\xb1\x82\xf1\x82\xc9\x82\xbf\x82\xcd\x90\xa2\x8a\x45")
	gb := []byte("\xc4\xe3\xba\xc3\xca\xc0\xbd\xe7\xd5\xe2\xca\xc7\xd6\xd0\xce\xc4")
	ru := []byte("\xcf\xf0\xe8\xe2\xe5\xf2 \xec\xe8\xf0 \xfd\xf2\xee \xf0\xf3\xf1\xf1\xea\xe8\xe9")
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"ascii", []byte("Hello"), "ascii"},
		{"utf8", []byte("Ol\xc3\xa1 n\xc3\xa3o \xc3\xa9 a\xc3\xa7\xc3\xa3o"), "utf-8"},
		{"short utf8", []byte("Ol\xc3\xa1"), "windows-1250"},
		{"utf8 bom", []byte("\xef\xbb\xbfHi"), "utf-8"},
		{"utf16le bom", []byte("\xff\xfeH\x00"), ""},
		{"utf16be bom", []byte("\xfe\xff\x00H"), ""},
		{"latin1", latin, "iso-8859-1"},
		{"shift-jis", sjis, "shift-jis"},
		{"gb", gb, "gb18030"},
		{"cyrillic", ru, "windows-1251"},
	} {
		if got := charencFor("x.srt", c.data); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDetectCharsetCorpus checks the detector against samples whose `charenc`
// the reference server was observed to pass (internal/transcode/testdata,
// probed black-box with one sidecar per sample, SubRip files).
func TestDetectCharsetCorpus(t *testing.T) {
	raw, err := os.ReadFile("testdata/charset-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Name      string `json:"name"`
		Hex       string `json:"hex"`
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus) != 84 {
		t.Fatalf("corpus has %d samples, want 84", len(corpus))
	}
	for _, c := range corpus {
		data, err := hex.DecodeString(c.Hex)
		if err != nil {
			t.Fatal(err)
		}
		got := charencFor("x.srt", data)
		if got == "" {
			got = "NONE"
		}
		if got != c.Reference {
			t.Errorf("%s: %q, reference %q", c.Name, got, c.Reference)
		}
	}
}

// The embedded tables must stay equal to the contract's data files.
func TestCharsetDataMatchesContract(t *testing.T) {
	for _, n := range []string{"charset-cjk.json", "charset-sbcs.json", "charset-statemachines.json"} {
		a, err := os.ReadFile("chardata/" + n)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile("../../docs/contract/data/" + n)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from docs/contract/data", n)
		}
	}
}

func TestCharsetBOMAndEdges(t *testing.T) {
	for _, c := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", ""},
		{"utf16be", "\xfe\xffab", "utf-16be"},
		{"ucs4-3412", "\xfe\xff\x00\x00", "X-ISO-10646-UCS-4-3412"},
		{"utf16le", "\xff\xfea\x00", "utf-16le"},
		{"utf32le", "\xff\xfe\x00\x00", "utf-32le"},
		{"utf32be", "\x00\x00\xfe\xff", "utf-32be"},
		{"ucs4-2143", "\x00\x00\xff\xfe", "X-ISO-10646-UCS-4-2143"},
		{"utf7", "+/v8", "utf-7"},
		{"gb18030 bom", "\x84\x31\x95\x33", "gb18030"},
		{"lone nbsp is plain", "a\xa0b", "ascii"},
		{"iso-2022-jp", "a\x1b$B$3$s$K$A$O\x1b(B", "iso-2022-jp"},
		{"hz", "a ~{<:Ky2;S{#,NpJ)l6HK!#~} b", "hz-gb-2312"},
		{"escape alone", "a\x1bb", "ascii"},
	} {
		if got := detectCharset([]byte(c.data)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Long inputs: the detector reads in chunks and stops at "found".
	long := strings.Repeat("Ol\xc3\xa1 mundo \xc3\xa7\xc3\xa3o. ", 200)
	if got := detectCharset([]byte(long)); got != "utf-8" {
		t.Errorf("long utf-8: %q", got)
	}
	if got := detectCharset([]byte(strings.Repeat("hello ", 1000))); got != "ascii" {
		t.Errorf("long ascii: %q", got)
	}
}

func TestCharencFor(t *testing.T) {
	bom := []byte("\xff\xfeH\x00")
	for ext, want := range map[string]string{"a.srt": "", "a.SSA": "", "a.ass": "", "a.sub": "utf-16le"} {
		if got := charencFor(ext, bom); got != want {
			t.Errorf("%s: %q, want %q", ext, got, want)
		}
	}
	if got := charencFor("a.srt", nil); got != "" {
		t.Errorf("empty: %q", got)
	}
}
