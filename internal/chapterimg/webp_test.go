package chapterimg

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The encoder's output is checked with an independent decoder (Pillow) when
// python3 has it; the test is skipped otherwise.
func TestEncodeWebP(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if err := exec.Command(py, "-I", "-c", "import PIL.Image").Run(); err != nil {
		t.Skip("no Pillow")
	}
	rnd := rand.New(rand.NewSource(1))
	cases := map[string]func(x, y int) color.RGBA{
		"flat":     func(x, y int) color.RGBA { return color.RGBA{10, 200, 30, 255} },
		"gradient": func(x, y int) color.RGBA { return color.RGBA{uint8(x * 3), uint8(y * 5), uint8(x + y), 255} },
		"noise": func(x, y int) color.RGBA {
			return color.RGBA{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), 255}
		},
		"two": func(x, y int) color.RGBA {
			if (x+y)%2 == 0 {
				return color.RGBA{0, 0, 0, 255}
			}
			return color.RGBA{255, 255, 255, 255}
		},
	}
	for name, f := range cases {
		for _, sz := range [][2]int{{1, 1}, {2, 1}, {37, 19}, {100, 56}} {
			img := image.NewRGBA(image.Rect(0, 0, sz[0], sz[1]))
			for y := 0; y < sz[1]; y++ {
				for x := 0; x < sz[0]; x++ {
					img.SetRGBA(x, y, f(x, y))
				}
			}
			var buf bytes.Buffer
			if err := encodeWebP(&buf, img); err != nil {
				t.Fatal(err)
			}
			if buf.Len()%2 != 0 {
				t.Errorf("%s %v: odd length", name, sz)
			}
			dir := t.TempDir()
			in := filepath.Join(dir, "a.webp")
			raw := filepath.Join(dir, "a.raw")
			if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			script := "import sys,PIL.Image as I;im=I.open(sys.argv[1]).convert('RGBA');open(sys.argv[2],'wb').write(im.tobytes());print(im.size[0],im.size[1])"
			out, err := exec.Command(py, "-I", "-c", script, in, raw).CombinedOutput()
			if err != nil {
				t.Errorf("%s %v: decode: %v %s", name, sz, err, out)
				continue
			}
			got, _ := os.ReadFile(raw)
			if !bytes.Equal(got, img.Pix) {
				t.Errorf("%s %v: pixels differ", name, sz)
			}
		}
	}
}

// A lossy WebP decodes (Pillow, libwebp) to the picture's size, close to the
// picture, and gets bigger as the quality goes up.
func TestEncodeVP8(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if err := exec.Command(py, "-I", "-c", "import PIL.Image").Run(); err != nil {
		t.Skip("no Pillow")
	}
	rnd := rand.New(rand.NewSource(2))
	pic := func(w, h int, f func(x, y int) color.RGBA) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.SetRGBA(x, y, f(x, y))
			}
		}
		return img
	}
	smooth := func(x, y int) color.RGBA {
		return color.RGBA{uint8(40 + x*180/300), uint8(60 + y*100/200), uint8(120 + (x+y)%40), 255}
	}
	cases := []struct {
		name string
		img  *image.RGBA
		min  float64 // PSNR at quality 90
	}{
		{"1x1", pic(1, 1, smooth), 30},
		{"odd", pic(37, 19, smooth), 30},
		{"smooth", pic(300, 200, smooth), 33},
		{"edges", pic(160, 90, func(x, y int) color.RGBA {
			if (x/20+y/15)%2 == 0 {
				return color.RGBA{230, 230, 230, 255}
			}
			return color.RGBA{20, 40, 160, 255}
		}), 24},
		{"noise", pic(64, 48, func(x, y int) color.RGBA {
			v := uint8(rnd.Intn(256))
			return color.RGBA{v, v, v, 255}
		}), 20},
	}
	script := "import sys,PIL.Image as I;im=I.open(sys.argv[1]).convert('RGBA');open(sys.argv[2],'wb').write(im.tobytes());print(im.size[0],im.size[1])"
	for _, c := range cases {
		prev := 0
		for _, q := range []int{5, 40, 90, 99} {
			var buf bytes.Buffer
			if err := encodeVP8(&buf, c.img, q); err != nil {
				t.Fatal(err)
			}
			b := buf.Bytes()
			if string(b[8:16]) != "WEBPVP8 " || len(b)%2 != 0 {
				t.Fatalf("%s q%d: not a lossy WebP", c.name, q)
			}
			dir := t.TempDir()
			in, raw := filepath.Join(dir, "a.webp"), filepath.Join(dir, "a.raw")
			os.WriteFile(in, b, 0o644)
			out, err := exec.Command(py, "-I", "-c", script, in, raw).CombinedOutput()
			if err != nil {
				t.Fatalf("%s q%d: decode: %v %s", c.name, q, err, out)
			}
			got, _ := os.ReadFile(raw)
			want := c.img.Pix
			if len(got) != len(want) {
				t.Fatalf("%s q%d: %d bytes decoded, want %d", c.name, q, len(got), len(want))
			}
			var se float64
			for i := 0; i < len(want); i += 4 {
				for k := 0; k < 3; k++ {
					d := float64(got[i+k]) - float64(want[i+k])
					se += d * d
				}
			}
			mse := se / float64(len(want)/4*3)
			psnr := 99.0
			if mse > 0 {
				psnr = 10 * math.Log10(255*255/mse)
			}
			t.Logf("%s q%d: %d bytes, PSNR %.1f", c.name, q, len(b), psnr)
			if q == 90 && psnr < c.min {
				t.Errorf("%s q%d: PSNR %.1f below %.1f", c.name, q, psnr, c.min)
			}
			if len(b) < prev && c.name != "1x1" {
				t.Errorf("%s: q%d is smaller (%d) than the quality before (%d)", c.name, q, len(b), prev)
			}
			prev = len(b)
		}
	}
}
