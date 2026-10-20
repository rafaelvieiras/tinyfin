package chapterimg

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// Overlay is what the overlay members of a picture request ask to be drawn on
// the picture once it has its final size, in this order: blur, the foreground
// layer, the played bar, the unplayed count. backgroundColor draws nothing on
// a picture without transparency; it only makes the picture be made again.
type Overlay struct {
	Blur    int     // sigma, 0 for none
	Layer   int     // alpha (0-255) of a black layer over the picture, -1 for none
	Percent float64 // 0 for no bar
	Count   int     // 0 for no count
}

// overlayOf reads the overlay members of a request. need is whether any of
// them makes the picture be made again (a value that is given and means
// something: blur whatever its value, a percentPlayed outside 0 to 100 and a
// 0 do not, a count of 0 or less does not, a backgroundColor that is not a
// hex colour does not, a foregroundLayer that is empty does not).
func overlayOf(p Params) (o Overlay, need bool) {
	o.Layer = -1
	if p.Blur != nil {
		need = true
		if *p.Blur > 0 {
			o.Blur = *p.Blur
		}
	}
	if pp := p.PercentPlayed; pp != nil && !(*pp <= 0 || *pp >= 100) {
		need = true
		if !math.IsNaN(*pp) {
			o.Percent = *pp
		}
	}
	if p.UnplayedCount != nil && *p.UnplayedCount > 0 {
		need = true
		o.Count = *p.UnplayedCount
	}
	if isHexColor(p.BackgroundColor) {
		need = true
	}
	if l := p.ForegroundLayer; l != "" {
		need = true
		// a whole number is the alpha of a black layer (its low byte; a
		// multiple of 256 makes the picture black); anything else is 60 %
		if n, err := strconv.ParseInt(strings.TrimSpace(l), 10, 32); err == nil {
			a := int(n) & 0xff
			if a == 0 {
				a = 256
			}
			o.Layer = a
		} else if f, err := strconv.ParseFloat(strings.TrimSpace(l), 64); err == nil && !strings.ContainsAny(l, "xXnNiI") {
			o.Layer = int(f*85) & 0xff
		} else {
			o.Layer = 153
		}
	}
	return o, need
}

func isHexColor(s string) bool {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 && len(s) != 8 {
		return false
	}
	_, err := strconv.ParseUint(s, 16, 32)
	return err == nil
}

func (o Overlay) draw(img *image.RGBA) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if o.Blur > 0 {
		blur(img, float64(o.Blur))
	}
	if o.Layer >= 0 {
		f := 1 - float64(min(o.Layer, 255))/255
		if o.Layer == 256 {
			f = 0
		}
		for i := 0; i+3 < len(img.Pix); i += 4 {
			img.Pix[i] = uint8(float64(img.Pix[i])*f + 0.5)
			img.Pix[i+1] = uint8(float64(img.Pix[i+1])*f + 0.5)
			img.Pix[i+2] = uint8(float64(img.Pix[i+2])*f + 0.5)
		}
	}
	if o.Percent > 0 {
		// 9 rows at the bottom: a 60 % black backdrop (not on the last
		// column) and the played part, solid blue, over it
		top := h - 9
		if top < 0 {
			top = 0
		}
		played := int(math.Floor(float64(w-1)*o.Percent/100 + 0.5))
		for y := top; y < h; y++ {
			for x := 0; x < w-1; x++ {
				if x < played {
					img.SetRGBA(x, y, color.RGBA{0, 164, 220, 255})
				} else {
					c := img.RGBAAt(x, y)
					img.SetRGBA(x, y, color.RGBA{uint8(float64(c.R) * 0.4), uint8(float64(c.G) * 0.4), uint8(float64(c.B) * 0.4), 255})
				}
			}
		}
	}
	if o.Count > 0 {
		cx, cy := float64(w-38), 38.0
		for y := 0; y < h; y++ {
			if math.Abs(float64(y)+0.5-cy) > 21 {
				continue
			}
			for x := 0; x < w; x++ {
				// coverage of the circle (radius 20, 80 % blue) by the
				// distance of the pixel centre from its edge
				d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
				cov := math.Max(0, math.Min(1, 20.5-d))
				if cov > 0 {
					blend(img, x, y, color.RGBA{0, 164, 220, 255}, 0.8*cov)
				}
			}
		}
		drawDigits(img, strconv.Itoa(o.Count), int(cx), int(cy))
	}
}

func blend(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	d := img.RGBAAt(x, y)
	mix := func(dst, src uint8) uint8 { return uint8(float64(dst)*(1-a) + float64(src)*a + 0.5) }
	img.SetRGBA(x, y, color.RGBA{mix(d.R, c.R), mix(d.G, c.G), mix(d.B, c.B), 255})
}

// digit shapes, 3 wide and 5 high
var glyphs = [10][5]string{
	{"###", "# #", "# #", "# #", "###"},
	{" # ", "## ", " # ", " # ", "###"},
	{"###", "  #", "###", "#  ", "###"},
	{"###", "  #", "###", "  #", "###"},
	{"# #", "# #", "###", "  #", "  #"},
	{"###", "#  ", "###", "  #", "###"},
	{"###", "#  ", "###", "# #", "###"},
	{"###", "  #", "  #", "  #", "  #"},
	{"###", "# #", "###", "# #", "###"},
	{"###", "# #", "###", "  #", "###"},
}

// drawDigits writes s in white, centred on (cx, cy), in blocks of 3 px (so a
// digit is 9 x 15 px; wider counts are drawn smaller to stay in the circle).
func drawDigits(img *image.RGBA, s string, cx, cy int) {
	scale := 3
	if len(s) > 2 {
		scale = 2
	}
	if len(s) > 4 {
		scale = 1
	}
	gap := scale
	total := len(s)*3*scale + (len(s)-1)*gap
	x0 := cx - total/2
	y0 := cy - 5*scale/2
	for i, r := range s {
		g := glyphs[r-'0']
		for gy := 0; gy < 5; gy++ {
			for gx := 0; gx < 3; gx++ {
				if g[gy][gx] != '#' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						x, y := x0+i*(3*scale+gap)+gx*scale+dx, y0+gy*scale+dy
						if image.Pt(x, y).In(img.Bounds()) {
							img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
						}
					}
				}
			}
		}
	}
}

// blur applies a gaussian blur of the given sigma, two passes with the same
// kernel. What lies outside the picture counts as black, so its borders go
// dark (a pixel on the edge keeps a little more than half of its value).
func blur(img *image.RGBA, sigma float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	r := int(math.Ceil(3 * sigma))
	if r > max(w, h) {
		r = max(w, h)
	}
	k := make([]float64, 2*r+1)
	var sum float64
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	src := make([]float64, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				src[(y*w+x)*3+c] = float64(img.Pix[y*img.Stride+x*4+c])
			}
		}
	}
	tmp := make([]float64, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc [3]float64
			for i := max(x-r, 0); i <= min(x+r, w-1); i++ {
				kv := k[i-x+r]
				for c := 0; c < 3; c++ {
					acc[c] += src[(y*w+i)*3+c] * kv
				}
			}
			copy(tmp[(y*w+x)*3:], acc[:])
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc [3]float64
			for j := max(y-r, 0); j <= min(y+r, h-1); j++ {
				kv := k[j-y+r]
				for c := 0; c < 3; c++ {
					acc[c] += tmp[(j*w+x)*3+c] * kv
				}
			}
			for c := 0; c < 3; c++ {
				img.Pix[y*img.Stride+x*4+c] = uint8(acc[c] + 0.5)
			}
		}
	}
}
