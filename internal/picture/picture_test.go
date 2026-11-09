package picture

import (
	"image"
	"image/color"
	"testing"
)

// newChecker is a black-and-white checkerboard, the pattern that shows whether
// a downscale averages its pixels or samples one of them.
func newChecker(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{A: 0xFF}
			if (x+y)%2 == 0 {
				c.R, c.G, c.B = 0xFF, 0xFF, 0xFF
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func intp(v int) *int { return &v }

// TestPlanFor covers the size arithmetic against the numbers the reference
// server answers with, one member at a time: the same table chapterimg's
// PlanFor test drives through its own wrapper, checked here at the source so a
// change to the shared rules is caught even if a wrapper stops exercising a
// case.
func TestPlanFor(t *testing.T) {
	cases := []struct {
		name     string
		p        Params
		sw, sh   int
		forces   bool
		rejected bool
		w, h     int
	}{
		{name: "nothing", sw: 640, sh: 360, w: 640, h: 360},
		{name: "maxWidth", p: Params{MaxWidth: intp(100)}, sw: 640, sh: 360, forces: true, w: 100, h: 56},
		{name: "maxHeight", p: Params{MaxHeight: intp(40)}, sw: 640, sh: 360, forces: true, w: 71, h: 40},
		{name: "tie goes to the even", p: Params{MaxWidth: intp(40)}, sw: 640, sh: 360, forces: true, w: 40, h: 22},
		{name: "width", p: Params{Width: intp(100)}, sw: 640, sh: 360, forces: true, w: 100, h: 56},
		{name: "width and height", p: Params{Width: intp(100), Height: intp(50)}, sw: 640, sh: 360, forces: true, w: 100, h: 50},
		{name: "tighter maximum wins", p: Params{MaxWidth: intp(100), MaxHeight: intp(30)}, sw: 640, sh: 360, forces: true, w: 53, h: 30},
		{name: "odd source", p: Params{MaxWidth: intp(200), MaxHeight: intp(64)}, sw: 650, sh: 366, forces: true, w: 114, h: 64},
		{name: "maximum above the size", p: Params{MaxWidth: intp(2000)}, sw: 120, sh: 68, w: 120, h: 68},
		{name: "maximum equal to the size", p: Params{MaxWidth: intp(120), MaxHeight: intp(68)}, sw: 120, sh: 68, w: 120, h: 68},
		{name: "zero maximum still asks for a picture", p: Params{MaxWidth: intp(0)}, sw: 640, sh: 360, forces: true, w: 640, h: 360},
		{name: "width above the size", p: Params{Width: intp(2000)}, sw: 120, sh: 68, forces: true, w: 120, h: 68},
		// A fill box alone reports Forces=false: it shapes the size for a
		// caller that has another reason to make the picture again, and
		// changes nothing otherwise.
		{name: "fill alone does not force", p: Params{FillWidth: intp(100)}, sw: 640, sh: 360, w: 100, h: 57},
		{name: "fill covers the box", p: Params{FillWidth: intp(300), FillHeight: intp(300)}, sw: 640, sh: 360, w: 534, h: 300},
		{name: "fill above the size is the size", p: Params{FillWidth: intp(900), FillHeight: intp(900)}, sw: 640, sh: 360, w: 640, h: 360},
		{name: "maximum wins over fill", p: Params{MaxWidth: intp(100), FillWidth: intp(300), FillHeight: intp(300)}, sw: 640, sh: 360, forces: true, w: 100, h: 56},
		{name: "a side that rounds to nothing", p: Params{Width: intp(640), Height: intp(54), MaxWidth: intp(5)}, sw: 640, sh: 360, forces: true, rejected: true, w: 640, h: 360},
	}
	for _, c := range cases {
		got := PlanFor(c.p, c.sw, c.sh)
		if got.Forces != c.forces || got.W != c.w || got.H != c.h || got.Rejected != c.rejected {
			t.Errorf("%s: got {forces:%v %dx%d rejected:%v}, want {forces:%v %dx%d rejected:%v}",
				c.name, got.Forces, got.W, got.H, got.Rejected, c.forces, c.w, c.h, c.rejected)
		}
	}
}

// TestResizeKeepsTheAspectRatioAndAverage pins the two properties the resizer
// is there for: the answer has exactly the asked size, and a shrinking scale
// averages pixels instead of picking one (so a checkerboard comes out grey
// rather than striped).
func TestResizeKeepsTheAspectRatioAndAverage(t *testing.T) {
	src := newChecker(100, 100)
	got := Resize(src, 10, 10)
	if b := got.Bounds(); b.Dx() != 10 || b.Dy() != 10 {
		t.Fatalf("bounds = %v, want 10x10", b)
	}
	for _, p := range []image.Point{{0, 0}, {5, 5}, {9, 9}} {
		c := got.RGBAAt(p.X, p.Y)
		if c.R < 100 || c.R > 155 {
			t.Errorf("pixel %v = %v, want the average of black and white", p, c)
		}
	}
}
