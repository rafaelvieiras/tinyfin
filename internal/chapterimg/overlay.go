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

func blend(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	d := img.RGBAAt(x, y)
	mix := func(dst, src uint8) uint8 { return uint8(float64(dst)*(1-a) + float64(src)*a + 0.5) }
	img.SetRGBA(x, y, color.RGBA{mix(d.R, c.R), mix(d.G, c.G), mix(d.B, c.B), 255})
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

// blur blurs the picture the way the reference does (Skia's blur): three box
// passes along each axis, rows first, whose window is
// floor(sigma * 3 * sqrt(2 pi) / 4 + 0.5) (at least 1). An odd window is
// centred; an even one is passed twice as it is, the second time shifted by
// one pixel, and then once as the window plus one (the SVG recipe). The picture
// is extended by mirroring (not by black or by its edge pixels). The reference
// computes in 8 bits; here the passes are exact and rounded at the end, which
// stays within a level or two. For a window of 256 or more the reference
// switches to something wider that could not be worked out; the passes are
// then run twice, which has about the right width but not the same pixels.
func blur(img *image.RGBA, sigma float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	d := int(math.Floor(sigma*3*math.Sqrt(2*math.Pi)/4 + 0.5))
	if d < 1 {
		d = 1
	}
	type pass struct{ size, off int }
	var passes []pass
	if d%2 == 1 {
		passes = []pass{{d, d / 2}, {d, d / 2}, {d, d / 2}}
	} else {
		passes = []pass{{d, d / 2}, {d, d/2 - 1}, {d + 1, d / 2}}
	}
	if d >= 256 {
		passes = append(passes, passes...)
	}
	buf := make([]float64, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				buf[(y*w+x)*3+c] = float64(img.Pix[y*img.Stride+x*4+c])
			}
		}
	}
	for axis := 0; axis < 2; axis++ {
		n, lines, step, lstep := w, h, 3, w*3
		if axis == 1 {
			n, lines, step, lstep = h, w, w*3, 3
		}
		line := make([]float64, n)
		pre := make([]float64, 0)
		for l := 0; l < lines; l++ {
			for c := 0; c < 3; c++ {
				base := l*lstep + c
				for i := 0; i < n; i++ {
					line[i] = buf[base+i*step]
				}
				for _, p := range passes {
					if p.size == 1 {
						continue
					}
					// prefix sums over the line extended by p.size on both sides
					m := n + 2*p.size
					if cap(pre) < m+1 {
						pre = make([]float64, m+1)
					}
					pre = pre[:m+1]
					pre[0] = 0
					for i := 0; i < m; i++ {
						pre[i+1] = pre[i] + line[mirror(i-p.size, n)]
					}
					for i := 0; i < n; i++ {
						lo := i + p.size - p.off
						line[i] = (pre[lo+p.size] - pre[lo]) / float64(p.size)
					}
				}
				for i := 0; i < n; i++ {
					buf[base+i*step] = line[i]
				}
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				img.Pix[y*img.Stride+x*4+c] = uint8(math.Min(255, math.Max(0, buf[(y*w+x)*3+c]+0.5)))
			}
		}
	}
}

// mirror maps any index onto 0..n-1 by mirroring at the edges, the edge pixel
// repeated (... 1 0 | 0 1 2 ... n-1 | n-1 n-2 ...).
func mirror(i, n int) int {
	p := 2 * n
	i %= p
	if i < 0 {
		i += p
	}
	if i >= n {
		i = p - 1 - i
	}
	return i
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
