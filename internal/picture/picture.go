// Package picture is the arithmetic of the picture routes: what size a request
// asks a stored picture to be, and how to scale a picture to it.
//
// It holds no HTTP, no storage and no encoding, because three different
// callers need the same answers: the item-image route (a poster or a backdrop
// asked for at card size), the chapter-image route (a thumbnail rendered by
// the video package and then shaped by the same query members) and, through
// chapterimg, the overlay variants of both. The rules below were measured
// against the reference server, one query member at a time; chapterimg's doc
// comments carry the measurements.
//
// What the reference does, and this reproduces: a picture is resized only when
// the request actually changes it — a maxWidth or maxHeight below the stored
// size, a width or height different from it, a fill box that is smaller — and
// is never enlarged. A request that asks for nothing, or for something bigger
// than the stored picture, is answered with the stored bytes unchanged.
package picture

import (
	"image"
	"image/color"
	"math"
)

// Params are the size members of a picture request. A nil member was not
// given.
type Params struct {
	MaxWidth, MaxHeight   *int
	Width, Height         *int
	FillWidth, FillHeight *int
}

// Plan is the size a request asks a picture to come out at, and whether the
// size members alone are reason enough to make the picture again.
type Plan struct {
	// W and H are the size to produce *if* the picture is made again, with
	// the fill box applied. A caller that decides not to (Forces is false and
	// nothing else asked) serves the stored file and ignores them: chapterimg
	// re-encodes when a quality or a format asks, and the fill shapes the
	// result then, which is exactly what the reference does.
	W, H int
	// Forces reports that a size member asks for a picture that is not the
	// stored one — a maxWidth or maxHeight below the stored size, a width or
	// height that differs. It is what makes a route re-encode: a request that
	// only asks for a fill box, or for something larger than the picture,
	// reports false and the stored file is the answer.
	//
	// A size member that asks for the stored size exactly ("width=640" of a
	// 640-wide picture, "maxWidth=0") still counts as forcing: the reference
	// re-encodes there rather than passing the file through (chapterimg's
	// PlanFor tests pin it).
	Forces bool
	// Rejected reports that the size works out to nothing (a side below 1
	// pixel). The stored picture is the answer whatever else was asked for.
	Rejected bool
}

// PlanFor decides how a srcW x srcH picture is answered.
//
// The size is worked out step by step: width and height set the size (both:
// exactly, the aspect ratio is dropped; one: the other follows the ratio),
// maxHeight then maxWidth cap the result (each on the size before it),
// fillWidth and fillHeight make it cover that box (never by enlarging it), and
// last the picture is brought back inside its own size, keeping the ratio: a
// stored picture is never enlarged. A side that comes out as 0 leaves the
// picture as it is.
func PlanFor(p Params, srcW, srcH int) Plan {
	if srcW < 1 || srcH < 1 {
		return Plan{W: srcW, H: srcH}
	}
	mw, hasMW := val(p.MaxWidth)
	mh, hasMH := val(p.MaxHeight)
	w, hasW := val(p.Width)
	h, hasH := val(p.Height)
	fw, hasFW := val(p.FillWidth)
	fh, hasFH := val(p.FillHeight)
	hasFW = hasFW && fw != 0
	hasFH = hasFH && fh != 0
	// A negative fill value cancels the fill altogether.
	filling := (hasFW || hasFH) && fw >= 0 && fh >= 0
	// Only these four make the picture be made again by themselves: a fill box
	// on its own changes nothing, because the reference ignores one that
	// arrives without a size member beside it (measured: fillWidth=100 of a
	// 640x360 picture is answered with the stored file). The fill still shapes
	// the size once something else — a format or a quality — already asks for
	// a re-encode.
	forces := (hasMW && mw < srcW) || (hasMH && mh < srcH) || (hasW && w != srcW) || (hasH && h != srcH)

	cw, ch := float64(srcW), float64(srcH)
	curW, curH := srcW, srcH
	set := func(nw, nh int) {
		curW, curH = nw, nh
		cw, ch = float64(curW), float64(curH)
	}
	switch {
	case hasW && hasH && w > 0 && h > 0:
		set(w, h)
	case hasW && w > 0:
		set(w, follow(srcH, srcW, w))
	case hasH && h > 0:
		set(follow(srcW, srcH, h), h)
	}
	// maxHeight then maxWidth, each on the size the step before gave: a side
	// above its cap is set to the cap and the other follows the ratio of the
	// size at that point, rounded to the nearest integer (a tie going to the
	// even one).
	if hasMH && mh > 0 && mh < curH {
		set(follow(curW, curH, mh), mh)
	}
	if hasMW && mw > 0 && mw < curW {
		set(mw, follow(curH, curW, mw))
	}
	if filling {
		// The divisor is worked out first (the smaller of the ratios of the
		// current size to the asked one, so the box is covered) and the sides
		// are divided by it and rounded up.
		div := math.Inf(1)
		if hasFW {
			div = math.Min(div, cw/float64(fw))
		}
		if hasFH {
			div = math.Min(div, ch/float64(fh))
		}
		if div > 1 {
			nw, nh := int(math.Ceil(cw/div)), int(math.Ceil(ch/div))
			// The shorter side coming out as 1 takes the longer one from the
			// shape (rounded up), whatever was asked for it.
			if min(nw, nh) == 1 {
				if cw >= ch {
					nw, nh = int(math.Ceil(cw/ch)), 1
				} else {
					nw, nh = 1, int(math.Ceil(ch/cw))
				}
			}
			set(nw, nh)
		}
	}
	if curW > srcW || curH > srcH {
		f := math.Min(float64(srcW)/cw, float64(srcH)/ch)
		set(max(roundHalfEven(cw*f), 1), max(roundHalfEven(ch*f), 1))
	}
	if curW < 1 || curH < 1 {
		// A side that rounds to nothing leaves the picture as it is.
		return Plan{W: srcW, H: srcH, Forces: forces, Rejected: true}
	}
	return Plan{W: curW, H: curH, Forces: forces}
}

// Resize scales src to w x h by area averaging (a box filter on each axis):
// the same reduction a camera's downscale does, which keeps thumbnail-sized
// results clean where a nearest-neighbour or a plain bilinear read of a large
// poster leaves aliasing.
func Resize(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if sw == w && sh == h {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				dst.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)})
			}
		}
		return dst
	}
	// Horizontal pass into float rows, then vertical.
	rows := make([][4]float32, w*sh)
	xs := spans(sw, w)
	for y := 0; y < sh; y++ {
		line := make([][4]float32, sw)
		for x := 0; x < sw; x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			line[x] = [4]float32{float32(r >> 8), float32(g >> 8), float32(bl >> 8), float32(a >> 8)}
		}
		for x := 0; x < w; x++ {
			rows[y*w+x] = average(line, xs[x])
		}
	}
	ys := spans(sh, h)
	for x := 0; x < w; x++ {
		col := make([][4]float32, sh)
		for y := 0; y < sh; y++ {
			col[y] = rows[y*w+x]
		}
		for y := 0; y < h; y++ {
			v := average(col, ys[y])
			dst.SetRGBA(x, y, color.RGBA{clamp8(v[0]), clamp8(v[1]), clamp8(v[2]), clamp8(v[3])})
		}
	}
	return dst
}

// follow is the side that goes with n when a side of size from becomes n: the
// ratio n/from first, then the product with the other side, rounded to the
// nearest integer with a tie going to the even one. The order of the two
// operations matters (it decides the ties: 56 of 640x360 gives 31, 24 gives 14).
func follow(other, from, n int) int {
	return int(math.RoundToEven(float64(n) / float64(from) * float64(other)))
}

func val(p *int) (int, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

type span struct {
	from, to int
	wf, wt   float32 // weight of the first and last source pixel
}

// spans maps each of n destination pixels to the run of the src source
// pixels it covers, with the partial weights at both ends.
func spans(src, n int) []span {
	out := make([]span, n)
	scale := float64(src) / float64(n)
	for i := range out {
		a, b := float64(i)*scale, float64(i+1)*scale
		from, to := int(math.Floor(a)), int(math.Ceil(b))-1
		if to < from {
			to = from
		}
		if to >= src {
			to = src - 1
		}
		s := span{from: from, to: to, wf: 1, wt: 1}
		if from == to {
			s.wf, s.wt = 1, 1
		} else {
			s.wf = float32(float64(from+1) - a)
			s.wt = float32(b - float64(to))
		}
		out[i] = s
	}
	return out
}

func average(line [][4]float32, s span) [4]float32 {
	var sum [4]float32
	var tot float32
	for i := s.from; i <= s.to; i++ {
		wt := float32(1)
		if s.from != s.to {
			if i == s.from {
				wt = s.wf
			} else if i == s.to {
				wt = s.wt
			}
		}
		for c := 0; c < 4; c++ {
			sum[c] += line[i][c] * wt
		}
		tot += wt
	}
	if tot > 0 {
		for c := range sum {
			sum[c] /= tot
		}
	}
	return sum
}

func clamp8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func roundHalfEven(f float64) int { return int(math.RoundToEven(f)) }
