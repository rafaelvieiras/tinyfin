package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestBitDepthOfPixFmt(t *testing.T) {
	for pf, want := range map[string]int{
		"yuv420p": 8, "yuvj420p": 8, "gray": 8, "bgr0": 8, "nv12": 8, "yuva444p": 8,
		"yuv420p10le": 10, "gray12le": 12, "yuv420p9le": 9, "gbrp14le": 14, "yuv444p16le": 16, "p010le": 10,
		"gbrp": 0, "": 0,
	} {
		if got := bitDepthOfPixFmt(pf); got != want {
			t.Errorf("%q: %d, want %d", pf, got, want)
		}
	}
}

func TestNormalizeFormat(t *testing.T) {
	vp9 := storage.MediaStreamRef{Type: storage.StreamTypeVideo, Codec: "vp9"}
	opus := storage.MediaStreamRef{Type: storage.StreamTypeAudio, Codec: "opus"}
	aac := storage.MediaStreamRef{Type: storage.StreamTypeAudio, Codec: "aac"}
	sub := storage.MediaStreamRef{Type: storage.StreamTypeSubtitle, Codec: "subrip"}
	for name, c := range map[string]struct {
		format  string
		streams []storage.MediaStreamRef
		want    string
	}{
		"webm streams": {"matroska,webm", []storage.MediaStreamRef{vp9, opus}, "mkv,webm"},
		"aac":          {"matroska,webm", []storage.MediaStreamRef{vp9, aac}, "mkv"},
		"subtitle":     {"matroska,webm", []storage.MediaStreamRef{vp9, opus, sub}, "mkv"},
		"no streams":   {"matroska,webm", nil, "mkv"},
		"transport":    {"mpegts", nil, "ts"},
		"mp4 family":   {"mov,mp4,m4a,3gp,3g2,mj2", nil, "mov,mp4,m4a,3gp,3g2,mj2"},
		"asf":          {"asf", nil, "asf"},
		"not probed":   {"", nil, ""},
	} {
		if got := normalizeFormat(c.format, c.streams); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestTransportStreamTimestamp(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, packets, stride int, prefix []byte) string {
		b := make([]byte, packets*stride)
		for i := 0; i < packets; i++ {
			o := i * stride
			if stride == 192 {
				copy(b[o:], prefix)
				o += 4
			}
			b[o] = 0x47
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, c := range map[string]struct {
		path string
		want string
	}{
		"plain":  {write("a.ts", 4, 188, nil), tsNone},
		"zero":   {write("b.m2ts", 4, 192, []byte{0, 0, 0, 0}), tsZero},
		"valid":  {write("c.m2ts", 4, 192, []byte{0x0e, 0xbf, 0x46, 0x22}), tsValid},
		"copy":   {write("d.m2ts", 4, 192, []byte{0xc0, 0, 0, 0}), tsValid},
		"absent": {filepath.Join(dir, "none.ts"), ""},
	} {
		if got := transportStreamTimestamp(c.path); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
