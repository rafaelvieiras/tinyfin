package libraryart

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// solid builds a w×h picture of one colour — enough to tell which band a
// pixel came from without depending on any real artwork.
func solid(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func compose(t *testing.T, name string, sources []image.Image) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := Compose(&buf, name, sources); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("decoding the composed PNG: %v", err)
	}
	return img
}

func TestComposeWritesSixteenByNinePNG(t *testing.T) {
	var buf bytes.Buffer
	if err := Compose(&buf, "Movies", []image.Image{solid(600, 900, color.RGBA{R: 0xC0, A: 0xFF})}); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got, want := buf.Bytes()[:8], []byte("\x89PNG\r\n\x1a\n"); !bytes.Equal(got, want) {
		t.Fatalf("encoded bytes start %q, want the PNG signature", got)
	}
	img, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decoding the composed PNG: %v", err)
	}
	if got, want := img.Bounds().Dx(), Width; got != want {
		t.Errorf("width = %d, want %d", got, want)
	}
	if got, want := img.Bounds().Dy(), Height; got != want {
		t.Errorf("height = %d, want %d", got, want)
	}
}

func TestComposeRejectsNoSources(t *testing.T) {
	var buf bytes.Buffer
	if err := Compose(&buf, "Movies", nil); err != ErrNoSources {
		t.Fatalf("err = %v, want %v", err, ErrNoSources)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes on the error path, want none", buf.Len())
	}
}

// Every pixel belongs to exactly one band: four solid sources come back as
// four vertical regions in order, each over the whole column between its own
// edges. Sampled below the slant's reach of the centre row so the name (drawn
// over the middle) cannot interfere.
func TestComposePlacesSourcesSideBySideInOrder(t *testing.T) {
	colors := []color.RGBA{
		{R: 0xFF, A: 0xFF}, {G: 0xFF, A: 0xFF}, {B: 0xFF, A: 0xFF}, {R: 0xFF, G: 0xFF, A: 0xFF},
	}
	sources := make([]image.Image, len(colors))
	for i, c := range colors {
		sources[i] = solid(400, 600, c)
	}

	img := compose(t, "", sources)

	// Row 40 is near the top, where every boundary has leaned left, but still
	// well inside each band's own span for a quarter split.
	const y = 40
	for i, want := range colors {
		// Sample the middle of band i for this row: its edges lean by at most
		// slant*Height/2 ≈ 59px, so the central half of the band is safe.
		x := (2*i + 1) * Width / (2 * len(colors))
		got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Errorf("pixel (%d,%d) = %v, want the source at band %d (%v)", x, y, got, i, want)
		}
	}
}

// The boundaries lean: the same split point between the first two sources sits
// further right at the bottom of the canvas than at the top. Without the lean,
// both rows would switch colour at the very same column.
func TestComposeBandEdgesSlantAcrossRows(t *testing.T) {
	img := compose(t, "", []image.Image{
		solid(400, 600, color.RGBA{R: 0xFF, A: 0xFF}),
		solid(400, 600, color.RGBA{G: 0xFF, A: 0xFF}),
	})

	firstGreenColumn := func(y int) int {
		for x := 0; x < Width; x++ {
			if c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); c.G > 0x80 {
				return x
			}
		}
		return -1
	}

	top, bottom := firstGreenColumn(5), firstGreenColumn(Height-6)
	if top < 0 || bottom < 0 {
		t.Fatalf("no second-band pixel found (top=%d, bottom=%d)", top, bottom)
	}
	if bottom-top < 80 {
		t.Errorf("the edge moved from x=%d (top) to x=%d (bottom), want a lean of at least 80px", top, bottom)
	}
}

// More sources than the mosaic can show are dropped rather than squeezed: the
// last drawn band still comes from one of the first MaxSources pictures.
func TestComposeUsesAtMostMaxSourcesPictures(t *testing.T) {
	sources := make([]image.Image, 0, MaxSources+3)
	for i := 0; i < MaxSources+3; i++ {
		sources = append(sources, solid(200, 300, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}))
	}
	// The picture that must never appear is the first one past the cap: paint
	// it pure red and check no pixel is red.
	for i := MaxSources; i < len(sources); i++ {
		sources[i] = solid(200, 300, color.RGBA{R: 0xFF, A: 0xFF})
	}

	img := compose(t, "", sources)
	for _, x := range []int{0, Width / 4, Width / 2, 3 * Width / 4, Width - 1} {
		if c := color.RGBAModel.Convert(img.At(x, Height/2+80)).(color.RGBA); c.R > 0x80 && c.G < 0x20 && c.B < 0x20 {
			t.Fatalf("pixel (%d,%d) = %v, want no contribution from the pictures past the cap", x, Height/2+80, c)
		}
	}
}

// A long name must still fit the card: it is shrunk, not clipped or crashed on.
func TestComposeFitsLongNames(t *testing.T) {
	long := "Uma Biblioteca Com Um Nome Bem Comprido Que Nao Caberia De Jeito Nenhum"
	img := compose(t, long, []image.Image{solid(400, 600, color.RGBA{A: 0xFF})})
	if img.Bounds().Dx() != Width || img.Bounds().Dy() != Height {
		t.Fatalf("bounds = %v, want %dx%d", img.Bounds(), Width, Height)
	}
	// White pixels inside the text area prove something was drawn.
	found := false
	for y := Height / 3; y < 2*Height/3 && !found; y++ {
		for x := 0; x < Width; x++ {
			if c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); c.R > 0xF0 && c.G > 0xF0 && c.B > 0xF0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no white text pixels found in the middle third of the canvas")
	}
}

// A blank name draws the mosaic only — the card still gets a picture.
func TestComposeWithoutNameDrawsNoText(t *testing.T) {
	img := compose(t, "   ", []image.Image{solid(400, 600, color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xFF})})
	if got, want := img.At(Width/2, Height/2), (color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xFF}); got != want {
		t.Errorf("centre pixel = %v, want the untouched source colour %v", got, want)
	}
}

// TestComposeNameMatchesReferenceMetrics pins the typography to what the
// reference server's own library images measure: the name "Anime" comes out
// 391px wide and 87px tall with the middle of its ink at y=260 of 540. Those
// three numbers are the whole reason this package embeds DejaVu Sans Bold
// instead of a Go font (a fifth narrower at the same cap height) and places the
// name by its ink box instead of the face's ascent and descent. See
// fonts/LICENSE-DejaVu.txt and docs/11's library-card row.
func TestComposeNameMatchesReferenceMetrics(t *testing.T) {
	img := compose(t, "Anime", []image.Image{solid(400, 600, color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xFF})})

	minX, minY, maxX, maxY := Width, Height, -1, -1
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			if c.R > 0xEB && c.G > 0xEB && c.B > 0xEB {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		t.Fatal("no white text pixels at all")
	}
	width, height := maxX-minX+1, maxY-minY+1
	centerX, centerY := (minX+maxX)/2, (minY+maxY)/2
	t.Logf("name metrics: %dx%d at (%d,%d), reference 391x87 at (480,260)", width, height, centerX, centerY)
	// Tolerances leave room for hinting and antialiasing, not for a different
	// face or a different anchor: the Go bold face is 20% narrower and the
	// ascent/descent anchor puts the name 8px lower.
	if width < 380 || width > 402 {
		t.Errorf("name width = %d, want within 380..402 (reference: 391)", width)
	}
	if height < 82 || height > 92 {
		t.Errorf("name height = %d, want within 82..92 (reference: 87)", height)
	}
	if centerX < 470 || centerX > 490 {
		t.Errorf("name centre x = %d, want within 470..490", centerX)
	}
	if centerY < 254 || centerY > 266 {
		t.Errorf("name centre y = %d, want within 254..266 (reference: 260)", centerY)
	}
}
