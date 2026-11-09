package libraryart

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"testing"
)

// TestManualPreview writes a sample card for eyeballing during development.
// Skipped unless LIBRARYART_PREVIEW is set; LIBRARYART_PREVIEW_FILES is a
// colon-separated list of pictures to use as the mosaic's sources.
func TestManualPreview(t *testing.T) {
	name := os.Getenv("LIBRARYART_PREVIEW")
	if name == "" {
		t.Skip("LIBRARYART_PREVIEW not set")
	}
	var imgs []image.Image
	for _, f := range strings.Split(os.Getenv("LIBRARYART_PREVIEW_FILES"), ":") {
		if f == "" {
			continue
		}
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		im, _, err := image.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		imgs = append(imgs, im)
	}
	out, err := os.Create("/tmp/preview-new.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := Compose(out, name, imgs); err != nil {
		t.Fatal(err)
	}
}
