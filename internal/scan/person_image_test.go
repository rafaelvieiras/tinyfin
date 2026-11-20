package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestResolvePersonImageSource covers where a credit photo is looked for: an
// http URL is kept, a dead config path is dropped, and the local .actors
// convention is found in both of its shapes.
func TestResolvePersonImageSource(t *testing.T) {
	folder := t.TempDir()
	video := filepath.Join(folder, "Movie (2020).mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile video: %v", err)
	}
	jpg := filepath.Join(folder, actorsFlat)
	writeTinyJPEG(t, jpg)

	flat := filepath.Join(folder, actorsFolder, "Brad Pitt.jpg")
	nested := filepath.Join(folder, actorsFolder, "Angelina Jolie", "folder.jpg")
	for _, p := range []string{flat, nested} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeTinyJPEG(t, p)
	}
	// A file outside the item's folder is not this item's photo.
	outside := filepath.Join(t.TempDir(), "someone.jpg")
	writeTinyJPEG(t, outside)

	cases := []struct {
		name  string
		thumb string
		want  string
	}{
		{"web url kept", "https://image.tmdb.org/t/p/w185/x.jpg", "https://image.tmdb.org/t/p/w185/x.jpg"},
		{"dead config path dropped", "/config/data/metadata/People/B/Brad Pitt/folder.jpg", flat},
		{"flat .actors file found", "", flat},
		{"nested .actors folder found", "", nested},
		{"thumb outside the folder is not this item's", outside, ""},
		{"nothing anywhere", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := "Brad Pitt"
			switch c.name {
			case "nested .actors folder found":
				name = "Angelina Jolie"
			case "nothing anywhere", "thumb outside the folder is not this item's":
				name = "Nobody At All"
			}
			if got := resolvePersonImageSource(video, name, c.thumb); got != c.want {
				t.Fatalf("source = %q, want %q", got, c.want)
			}
		})
	}
}

// TestResolvePersonImageSourcesDropsUnusable: a credit with no usable photo is
// left with no source at all, so the API stops advertising a picture that would
// answer 404.
func TestResolvePersonImageSourcesDropsUnusable(t *testing.T) {
	folder := t.TempDir()
	item := storage.Item{
		Path: filepath.Join(folder, "Movie.mkv"),
		People: []storage.Person{
			{Name: "Brad Pitt", ImageURL: "/config/data/metadata/People/B/Brad Pitt/folder.jpg"},
			{Name: "Someone Else", ImageURL: "https://image.tmdb.org/t/p/w185/y.jpg"},
		},
	}
	resolvePersonImageSources(&item)
	if item.People[0].ImageURL != "" {
		t.Errorf("dead path survived: %q", item.People[0].ImageURL)
	}
	if item.People[1].ImageURL != "https://image.tmdb.org/t/p/w185/y.jpg" {
		t.Errorf("web URL changed: %q", item.People[1].ImageURL)
	}
}

// actorsFlat is a helper name kept short for the table above.
const actorsFlat = ".actors/Brad Pitt.jpg"

// writeTinyJPEG writes a 1x1 JPEG at path.
func writeTinyJPEG(t *testing.T, path string) {
	t.Helper()
	data := []byte{
		0xFF, 0xD8, 0xFF, 0xDB, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08,
		0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0A, 0x0C, 0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C,
		0x19, 0x12, 0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A, 0x1C, 0x1C, 0x20,
		0x24, 0x2E, 0x27, 0x20, 0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29, 0x2C, 0x30,
		0x31, 0x34, 0x34, 0x34, 0x1F, 0x27, 0x39, 0x3D, 0x38, 0x32, 0x3C, 0x2E, 0x33, 0x34,
		0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01, 0x00, 0x01, 0x01, 0x01, 0x11, 0x00,
		0xFF, 0xC4, 0x00, 0x1F, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
		0x07, 0x08, 0x09, 0x0A, 0x0B, 0xFF, 0xC4, 0x00, 0x14, 0x10, 0x01, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF,
		0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, 0xD2, 0xCF, 0x20, 0xFF, 0xD9,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
