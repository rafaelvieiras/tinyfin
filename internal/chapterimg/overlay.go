package chapterimg

import (
	"encoding/hex"
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
			img.Pix[i+3] = uint8(255*(1-f) + float64(img.Pix[i+3])*f + 0.5)
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
					blend(img, x, y, color.RGBA{0, 0, 0, 255}, 0.6)
				}
			}
		}
	}
	if o.Count > 0 {
		// the circle has no anti-aliasing and is not an exact one: the
		// reference's own pixels are used (40 x 40, from 20 left of the centre
		// and 20 above it)
		for j, row := range circleMask {
			y := 18 + j
			if y >= h {
				break
			}
			for i := 0; i < len(row); i++ {
				if x := w - 58 + i; row[i] == '#' && x >= 0 {
					blend(img, x, y, color.RGBA{0, 164, 220, 255}, 0.8)
				}
			}
		}
		drawDigits(img, strconv.Itoa(o.Count), w-38)
	}
}

// blend draws the opaque colour c over the pixel with opacity a (the picture
// is premultiplied, and has an alpha only where a blur gave it one).
func blend(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	d := img.RGBAAt(x, y)
	mix := func(dst, src uint8) uint8 { return uint8(float64(dst)*(1-a) + float64(src)*a + 0.5) }
	img.SetRGBA(x, y, color.RGBA{mix(d.R, c.R), mix(d.G, c.G), mix(d.B, c.B), mix(d.A, 255)})
}

// drawDigits writes s in white on the unplayed count's circle, whose centre
// is cx pixels from the left. The reference draws the number with a sans
// typeface (hinted, so the glyphs sit on whole pixels): 24 pixels high for one
// or two digits, centred on the circle, 18 for three or more, which start at a
// fixed place 14 pixels left of the centre and run on (past the edge of the
// picture, which clips them). The coverage of each glyph is the measured one
// (glyphdata.go), so the pixels are the reference's.
func drawDigits(img *image.RGBA, s string, cx int) {
	var size, adv, start int
	switch len(s) {
	case 1:
		size, adv, start = 1, 0, cx-6
	case 2:
		size, adv, start = 1, 15, cx-12
	default:
		size, adv, start = 0, 11, cx-14
	}
	for i := 0; i < len(s); i++ {
		g := glyphData[size][s[i]-'0']
		pix, _ := hex.DecodeString(g.a)
		ox, oy := start+i*adv+g.x, 38+g.y
		for gy := 0; gy < g.h; gy++ {
			for gx := 0; gx < g.w; gx++ {
				a := float64(pix[gy*g.w+gx]) / 255
				if a == 0 || !image.Pt(ox+gx, oy+gy).In(img.Bounds()) {
					continue
				}
				blend(img, ox+gx, oy+gy, color.RGBA{255, 255, 255, 255}, a)
			}
		}
	}
}

// blur blurs the picture the way the reference does (Skia's blur). The picture
// is taken as opaque and as surrounded by nothing (transparent black), the
// colours premultiplied: the edges lose their alpha, so a JPEG (which has
// none) shows them dark and a PNG shows them half transparent. Along each
// axis, rows first, the blur is a few box passes over the picture extended
// with nothing (the passes spill beyond its edges and later passes read that,
// so the picture is extended once by the whole reach). The window is
// floor(sigma * 3 * sqrt(2 pi) / 4 + 0.5) (at least 1, the sigma at most
// 532). Below 256 it is three boxes of that window: odd ones centred, even
// ones as the SVG recipe says (the window twice, the second time shifted by
// one pixel, then the window plus one). From 256 on it is two boxes of
// window * 3 / 2 (the first centred, the second shifted by one when that is
// even). The reference computes in 8 bits; here the passes are exact and
// rounded at the end, which stays within a level or two.
func blur(img *image.RGBA, sigma float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	sigma = math.Min(sigma, 532)
	d := int(math.Floor(sigma*3*math.Sqrt(2*math.Pi)/4 + 0.5))
	if d < 1 {
		d = 1
	}
	type pass struct{ size, off int }
	var passes []pass
	switch {
	case d >= 256:
		d = d * 3 / 2
		if d%2 == 1 {
			passes = []pass{{d, d / 2}, {d, d / 2}}
		} else {
			passes = []pass{{d, d / 2}, {d, d/2 - 1}}
		}
	case d%2 == 1:
		passes = []pass{{d, d / 2}, {d, d / 2}, {d, d / 2}}
	default:
		passes = []pass{{d, d / 2}, {d, d/2 - 1}, {d + 1, d / 2}}
	}
	reach := 0
	for _, p := range passes {
		reach += p.size
	}
	buf := make([]float64, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 4; c++ {
				buf[(y*w+x)*4+c] = float64(img.Pix[y*img.Stride+x*4+c])
			}
		}
	}
	var line, pre []float64
	for axis := 0; axis < 2; axis++ {
		n, lines, step, lstep := w, h, 4, w*4
		if axis == 1 {
			n, lines, step, lstep = h, w, w*4, 4
		}
		m := n + 2*reach
		if cap(line) < m {
			line = make([]float64, m)
			pre = make([]float64, m+1)
		}
		line, pre = line[:m], pre[:m+1]
		for l := 0; l < lines; l++ {
			for c := 0; c < 4; c++ {
				base := l*lstep + c
				for i := range line {
					line[i] = 0
				}
				for i := 0; i < n; i++ {
					line[reach+i] = buf[base+i*step]
				}
				for _, p := range passes {
					for i := 0; i < m; i++ {
						pre[i+1] = pre[i] + line[i]
					}
					for i := 0; i < m; i++ {
						lo, hi := min(max(i-p.off, 0), m), min(max(i-p.off+p.size, 0), m)
						line[i] = (pre[hi] - pre[lo]) / float64(p.size)
					}
				}
				for i := 0; i < n; i++ {
					buf[base+i*step] = line[reach+i]
				}
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := y*img.Stride + x*4
			// premultiplied: the colours cannot exceed the alpha
			a := uint8(math.Min(255, buf[(y*w+x)*4+3]+0.5))
			for c := 0; c < 3; c++ {
				img.Pix[o+c] = uint8(math.Min(float64(a), buf[(y*w+x)*4+c]+0.5))
			}
			img.Pix[o+3] = a
		}
	}
}

// circleMask is the unplayed count's circle (radius 20, centred 38 pixels from
// the top and from the right edge) as the reference rasterises it.
var circleMask = [40]string{
	"................########................",
	"............################............",
	"..........####################..........",
	".........######################.........",
	".......##########################.......",
	"......############################......",
	".....##############################.....",
	"....###############################.....",
	"....################################....",
	"...##################################...",
	"..###################################...",
	"..####################################..",
	".######################################.",
	".######################################.",
	".######################################.",
	".######################################.",
	"########################################",
	"########################################",
	"########################################",
	"########################################",
	"########################################",
	"########################################",
	"########################################",
	"########################################",
	".######################################.",
	".######################################.",
	".######################################.",
	".######################################.",
	"..####################################..",
	"..###################################...",
	"...##################################...",
	"....################################....",
	"....###############################.....",
	".....##############################.....",
	"......############################......",
	"........########################........",
	".........######################.........",
	"...........##################...........",
	"............################............",
	"................########................",
}
