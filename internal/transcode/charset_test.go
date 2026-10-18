package transcode

import "testing"

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
		{"utf8", []byte("Ol\xc3\xa1"), "utf-8"},
		{"utf8 bom", []byte("\xef\xbb\xbfHi"), "utf-8"},
		{"utf16le bom", []byte("\xff\xfeH\x00"), ""},
		{"utf16be bom", []byte("\xfe\xff\x00H"), ""},
		{"latin1", latin, "iso-8859-1"},
		{"shift-jis", sjis, "shift-jis"},
		{"gb", gb, "gb18030"},
		{"cyrillic", ru, "windows-1251"},
	} {
		if got := detectCharset(c.data); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
