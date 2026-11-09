package chapterimg

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/picture"
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

func PlanFor(p Params, srcW, srcH int) Plan {
	plan := Plan{W: srcW, H: srcH, Quality: defaultQuality, WebPQuality: -1}
	of := formatOf(p.Format)
	plan.PNG = of == fmtPNG
	plan.WebP = of == fmtWebP
	if of == fmtSVG {
		plan.Passthrough = true
		return plan
	}
	// The size arithmetic is shared with the item-image route, which answers
	// posters and backdrops to the same query members (internal/picture).
	size := picture.PlanFor(picture.Params{
		MaxWidth:   p.MaxWidth,
		MaxHeight:  p.MaxHeight,
		Width:      p.Width,
		Height:     p.Height,
		FillWidth:  p.FillWidth,
		FillHeight: p.FillHeight,
	}, srcW, srcH)

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
	if size.Forces {
		need = true
	}
	if !need {
		plan.Passthrough = true
		return plan
	}
	if size.Rejected {
		// the size works out to nothing: the stored picture is the answer,
		// whatever format was asked for
		plan.Passthrough = true
		plan.PNG, plan.WebP = false, false
		return plan
	}
	// no format asked for (or one that is not a name): a caller that takes
	// WebP gets it once the picture is made again
	if p.Webp && (of == fmtKeep) {
		plan.WebP = true
	}
	plan.W, plan.H = size.W, size.H
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
	dst := picture.Resize(src, plan.W, plan.H)
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
