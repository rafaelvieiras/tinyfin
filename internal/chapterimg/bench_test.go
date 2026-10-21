package chapterimg

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// WEBP_BENCH_DIR=<dir with .png>: writes <name>.ll.webp (lossless) and
// <name>.q<Q>.webp (lossy, WEBP_BENCH_Q) next to each picture. Not a test.
func TestWebPBench(t *testing.T) {
	dir := os.Getenv("WEBP_BENCH_DIR")
	if dir == "" {
		t.Skip("WEBP_BENCH_DIR not set")
	}
	q, _ := strconv.Atoi(os.Getenv("WEBP_BENCH_Q"))
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	for _, f := range files {
		fh, _ := os.Open(f)
		im, _, err := image.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		rgba := image.NewRGBA(im.Bounds())
		draw.Draw(rgba, rgba.Bounds(), im, im.Bounds().Min, draw.Src)
		base := f[:len(f)-4]
		var b bytes.Buffer
		if err := encodeWebP(&b, rgba); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(base+".ll.webp", b.Bytes(), 0o644)
		if q > 0 {
			b.Reset()
			if err := encodeVP8(&b, rgba, q); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(base+".q"+strconv.Itoa(q)+".webp", b.Bytes(), 0o644)
		}
	}
}
