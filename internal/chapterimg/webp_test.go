package chapterimg

import (
	"bytes"
	"image"
	"image/color"
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
