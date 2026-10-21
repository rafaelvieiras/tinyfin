package chapterimg

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Params are the picture-route query members that shape the answer for a
// chapter picture. A nil member was not given.
type Params struct {
	MaxWidth, MaxHeight   *int
	Width, Height         *int
	FillWidth, FillHeight *int
	Quality               *int
	// Format is the requested output format as sent ("Png", "jpg", ...).
	Format string
	// Blur, PercentPlayed, UnplayedCount, BackgroundColor and ForegroundLayer
	// are the overlay members (see overlay.go); a nil/empty one was not given.
	Blur          *int
	PercentPlayed *float64
	UnplayedCount *int
	// BackgroundColor and ForegroundLayer are the values as sent.
	BackgroundColor, ForegroundLayer string
	// Webp is true when the caller accepts image/webp (the Accept header).
	Webp bool
}

const (
	// defaultQuality is the JPEG quality of a picture that is re-encoded
	// without a quality being asked for.
	defaultQuality = 100
	// passQuality is the quality at and above which a picture that needs
	// no other change is served as it is.
	passQuality = 90
)

type outFormat int

const (
	fmtKeep         outFormat = iota // serve the stored format (JPEG)
	fmtJPEG                          // re-encode as JPEG
	fmtExplicitJPEG                  // jpg asked for: the stored format, whatever Accept says
	fmtPNG
	fmtWebP
	fmtSVG // never processed
)

// formatOf maps the requested format (a name in any case or its number 0 to 5:
// Bmp, Gif, Jpg, Png, Webp, Svg). bmp, gif and webp are known but cannot be
// produced here; they come out as a re-encoded JPEG. svg leaves the picture
// alone whatever else is asked. Anything else (jpg, unknown words, empty)
// leaves the format as it is.
func formatOf(s string) outFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "png", "3":
		return fmtPNG
	case "webp", "4":
		return fmtWebP
	case "bmp", "gif", "0", "1":
		return fmtJPEG
	case "jpg", "2":
		return fmtExplicitJPEG
	case "svg", "5":
		return fmtSVG
	}
	return fmtKeep
}

// ValidFormat reports whether a format value can be bound: a name (anything
// that is not a number is taken as unknown and ignored) or a number 0 to 5.
func ValidFormat(s string) bool {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return err != nil || (n >= 0 && n <= 5)
}

// Plan is what a request needs done to a picture of a given size.
type Plan struct {
	// Passthrough means the stored file is the answer.
	Passthrough bool
	W, H        int
	Quality     int
	PNG         bool
	WebP        bool
	// WebPQuality is the quality of a lossy WebP (0 to 99), -1 for the
	// lossless one; WebPEmpty is a WebP asked for with a quality outside 0 to
	// 100, which the reference answers with an empty body.
	WebPQuality int
	WebPEmpty   bool
	Overlay     Overlay
}

// ContentType is the media type of the answer.
func (p Plan) ContentType() string {
	if p.PNG {
		return "image/png"
	}
	if p.WebP {
		return "image/webp"
	}
	return "image/jpeg"
}

func val(p *int) (int, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// follow is the side that goes with n when a side of size from becomes n: the
// ratio n/from first, then the product with the other side, rounded to the
// nearest integer with a tie going to the even one. The order of the two
// operations matters (it decides the ties: 56 of 640x360 gives 31, 24 gives 14).
func follow(other, from, n int) int {
	return int(math.RoundToEven(float64(n) / float64(from) * float64(other)))
}

// PlanFor decides how a srcW x srcH picture is answered.
//
// The stored file is served as it is unless the request changes it: a
// maxWidth/maxHeight below the picture's size, a width/height different from
// it, a quality below 90 or a format that is not the stored one. Otherwise
// the size is worked out step by step: width and height set the size (both:
// exactly, the aspect ratio is dropped; one: the other follows the ratio),
// maxHeight then maxWidth cap the result (each on the size before it),
// fillWidth and fillHeight make it cover that box (never by enlarging it),
// and last the picture is brought back inside its own size, keeping the
// ratio: a stored picture is never enlarged. A side that comes out as 0
// before that last step leaves the picture as it is.
func PlanFor(p Params, srcW, srcH int) Plan {
	plan := Plan{W: srcW, H: srcH, Quality: defaultQuality, WebPQuality: -1}
	of := formatOf(p.Format)
	plan.PNG = of == fmtPNG
	plan.WebP = of == fmtWebP
	if of == fmtSVG {
		plan.Passthrough = true
		return plan
	}
	need := of != fmtKeep && of != fmtExplicitJPEG
	ov, needOv := overlayOf(p)
	plan.Overlay = ov
	need = need || needOv
	if q, ok := val(p.Quality); ok {
		if q >= 0 && q < 100 {
			plan.WebPQuality = q
		} else if q != 100 {
			plan.WebPEmpty = true
		}
		if q < 1 {
			q = 1
		}
		if q > 100 {
			q = 100
		}
		plan.Quality = q
		if q < passQuality {
			need = true
		}
	}
	mw, hasMW := val(p.MaxWidth)
	mh, hasMH := val(p.MaxHeight)
	w, hasW := val(p.Width)
	h, hasH := val(p.Height)
	if (hasMW && mw < srcW) || (hasMH && mh < srcH) || (hasW && w != srcW) || (hasH && h != srcH) {
		need = true
	}
	if !need {
		plan.Passthrough = true
		return plan
	}
	// no format asked for (or one that is not a name): a caller that takes
	// WebP gets it once the picture is made again
	if p.Webp && (of == fmtKeep) {
		plan.WebP = true
	}
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
	// size at that point, rounded to the nearest integer (a tie going to the even one).
	if hasMH && mh > 0 && mh < curH {
		set(follow(curW, curH, mh), mh)
	}
	if hasMW && mw > 0 && mw < curW {
		set(mw, follow(curH, curW, mw))
	}
	fw, hasFW := val(p.FillWidth)
	fh, hasFH := val(p.FillHeight)
	hasFW = hasFW && fw != 0
	hasFH = hasFH && fh != 0
	// a negative fill value cancels the fill altogether
	if (hasFW || hasFH) && fw >= 0 && fh >= 0 {
		// the divisor is worked out first (the smaller of the ratios of the
		// current size to the asked one, so the box is covered) and the sides
		// are divided by it and rounded up
		div := math.Inf(1)
		if hasFW {
			div = math.Min(div, cw/float64(fw))
		}
		if hasFH {
			div = math.Min(div, ch/float64(fh))
		}
		if div > 1 {
			nw, nh := int(math.Ceil(cw/div)), int(math.Ceil(ch/div))
			// the shorter side coming out as 1 takes the longer one from the
			// shape (rounded up), whatever was asked for it
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
		// a side that rounds to nothing leaves the picture as it is
		plan.Passthrough = true
		plan.PNG, plan.WebP = false, false
		return plan
	}
	plan.W, plan.H = curW, curH
	return plan
}

// Render returns the file holding the answer for a picture: the stored file
// itself when the plan says so, else a re-encoded copy kept next to it (and
// removed with it when the item's pictures are made again).
func (g *Generator) Render(id storage.ItemID, path string, mod time.Time, p Params) (file string, contentType string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil {
		return "", "", err
	}
	plan := PlanFor(p, cfg.Width, cfg.Height)
	if plan.Passthrough {
		return path, "image/jpeg", nil
	}
	ext := ".jpg"
	if plan.PNG {
		ext = ".png"
	} else if plan.WebP {
		ext = ".webp"
	}
	key := sha1.Sum([]byte(fmt.Sprintf("v3|%s|%d|%d|%d|%d|%v|%v|%d|%v|%+v", path, mod.UnixNano(), plan.W, plan.H, plan.Quality, plan.PNG, plan.WebP, plan.WebPQuality, plan.WebPEmpty, plan.Overlay)))
	dir := filepath.Join(g.Dir(id), "cache")
	out := filepath.Join(dir, hex.EncodeToString(key[:8])+ext)
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, plan.ContentType(), nil
	}
	f, err = os.Open(path)
	if err != nil {
		return "", "", err
	}
	src, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		return "", "", err
	}
	dst := resize(src, plan.W, plan.H)
	plan.Overlay.draw(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, "part-*"+ext)
	if err != nil {
		return "", "", err
	}
	if plan.PNG {
		err = png.Encode(tmp, dst)
	} else if plan.WebP {
		if plan.WebPEmpty {
			err = nil
		} else if plan.WebPQuality >= 0 {
			err = encodeVP8(tmp, dst, plan.WebPQuality)
		} else {
			err = encodeWebP(tmp, dst)
		}
	} else {
		err = jpeg.Encode(tmp, dst, &jpeg.Options{Quality: plan.Quality})
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), out)
	}
	if err == nil {
		// the copy is as old as the still, so Last-Modified is stable
		_ = os.Chtimes(out, mod, mod)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", "", err
	}
	return out, plan.ContentType(), nil
}

// resize scales src to w x h by area averaging (a box filter on each axis).
func resize(src image.Image, w, h int) *image.RGBA {
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
	// horizontal pass into float rows, then vertical
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

// ParseInt reads a query value as the picture route does: an integer that
// fits 32 bits. An empty value is "not given".
func ParseInt(s string) (v *int, given bool, ok bool) {
	if s == "" {
		return nil, false, true
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return nil, true, false
	}
	i := int(n)
	return &i, true, true
}

func roundHalfEven(f float64) int { return int(math.RoundToEven(f)) }
