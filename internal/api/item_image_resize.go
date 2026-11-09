// item_image_resize.go answers GET /Items/{itemId}/Images/{imageType} at the
// size the request asks for.
//
// The size members are the reference server's, measured one at a time against
// it (chapterimg documents the same measurements for the same members on
// chapter pictures, and internal/picture holds the arithmetic both use):
// maxWidth and maxHeight cap the picture, width and height set it outright,
// fillWidth and fillHeight make it cover a box, quality re-encodes a JPEG, and
// a stored picture is never enlarged. A request that asks for nothing — or for
// something bigger than the file — is answered with the file itself, byte for
// byte, exactly as before this existed.
//
// Two details are the reference's and are reproduced rather than guessed: the
// answer keeps the source's format (a PNG poster stays a PNG, so a resized
// library card is still a PNG), and the JPEG quality of a picture made again
// without a quality being asked for is 100 (measured: the same bytes as
// quality=100, four times the size of quality=90).
package api

import (
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/picture"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const (
	// defaultItemImageQuality is what a picture is re-encoded at when the
	// request does not say: the reference's own default, confirmed by byte
	// count against its answers at quality=90 and quality=100.
	defaultItemImageQuality = 100
	// itemImagePassQuality is the quality at and above which a picture that
	// needs no other change is served as it is — the same threshold chapter
	// pictures use, because it is the same reference behavior.
	itemImagePassQuality = 90
)

// itemImageSizing is the size a request asks for, before it is compared with
// the stored picture.
type itemImageSizing struct {
	picture.Params
	Quality *int
}

// parseItemImageSizing reads the size members off the query. A member that is
// not an integer is a validation error, as on the reference (which answers 400
// with a validation problem body); a member that is out of range is not, and is
// left to the plan (a negative maxWidth changes nothing).
func parseItemImageSizing(r *http.Request) (itemImageSizing, map[string][]string) {
	q := r.URL.Query()
	var s itemImageSizing
	errs := map[string][]string{}
	for _, f := range []struct {
		name string
		dst  **int
	}{
		{"maxWidth", &s.MaxWidth},
		{"maxHeight", &s.MaxHeight},
		{"width", &s.Width},
		{"height", &s.Height},
		{"fillWidth", &s.FillWidth},
		{"fillHeight", &s.FillHeight},
		{"quality", &s.Quality},
	} {
		raw := strings.TrimSpace(q.Get(f.name))
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			errs[f.name] = []string{"The value '" + raw + "' is not valid."}
			continue
		}
		v := int(n)
		*f.dst = &v
	}
	return s, errs
}

// itemImagePlan works out what the answer for ref is. ok=false means "serve the
// stored file": the request asks for nothing, or for something the file already
// is.
func (s *Server) itemImagePlan(sizing itemImageSizing, ref storage.ImageRef) (plan picture.Plan, quality int, format string, ok bool) {
	quality = defaultItemImageQuality
	if sizing.Quality != nil {
		quality = min(max(*sizing.Quality, 1), 100)
	}
	srcW, srcH := ref.Width, ref.Height
	format = formatOfPath(ref.Path)
	if srcW < 1 || srcH < 1 || format == "" {
		// A ref written before the scanner recorded dimensions or extensions
		// is measured and identified from the file itself.
		f, err := os.Open(ref.Path)
		if err != nil {
			return plan, quality, format, false
		}
		cfg, name, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			return plan, quality, format, false
		}
		format = name
		if srcW < 1 || srcH < 1 {
			srcW, srcH = cfg.Width, cfg.Height
		}
	}
	plan = picture.PlanFor(sizing.Params, srcW, srcH)
	if plan.Rejected || plan.W < 1 || plan.H < 1 {
		return plan, quality, format, false
	}
	// A quality below the pass threshold makes the picture again at its own
	// size, the rule the reference applies to a JPEG.
	if !plan.Forces && !(sizing.Quality != nil && quality < itemImagePassQuality) {
		return plan, quality, format, false
	}
	return plan, quality, format, true
}

// formatOfPath names a picture format from a file name, for the extensions the
// scanner catalogs. An empty answer means "look inside the file".
func formatOfPath(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	switch strings.ToLower(path[i+1:]) {
	case "jpg", "jpeg", "tbn":
		return "jpeg"
	case "png":
		return "png"
	case "gif":
		return "gif"
	case "webp":
		return "webp"
	}
	return ""
}

// serveResizedItemImage answers the request with the picture at plan's size,
// from the cache when that size was asked for before. It reports whether it
// answered: a failure falls back to the stored bytes rather than an error, so a
// picture always comes back.
func (s *Server) serveResizedItemImage(w http.ResponseWriter, r *http.Request, ref storage.ImageRef, itemID storage.ItemID, kind storage.ImageKind, index int, sourceInfo os.FileInfo, plan picture.Plan, quality int, format string) bool {
	// The answer keeps the source's format: a PNG stays PNG, everything else
	// (JPEG, GIF, WebP, a tbn holding any of them) is answered as JPEG, which
	// is what the item-image route has always served.
	outFormat, ext := "jpeg", ".jpg"
	if format == "png" {
		outFormat, ext = "png", ".png"
	}

	file, ok := s.openResizedItemImage(ref, itemID, kind, index, sourceInfo, plan, quality, outFormat, ext)
	if !ok {
		return false
	}
	defer file.Close()

	w.Header().Set("Content-Type", "image/"+outFormat)
	// Last-Modified is the stored picture's own, like the passthrough path:
	// the answer changes when the picture does, and conditional requests keep
	// working.
	http.ServeContent(w, r, "", sourceInfo.ModTime(), file)
	return true
}

// openResizedItemImage returns the picture at plan's size, from the cache when
// it is there, made into a temporary file when the cache is off or a write
// fails.
func (s *Server) openResizedItemImage(ref storage.ImageRef, itemID storage.ItemID, kind storage.ImageKind, index int, sourceInfo os.FileInfo, plan picture.Plan, quality int, outFormat, ext string) (*os.File, bool) {
	write := func(out io.Writer) error {
		return s.writeResizedImage(ref, plan, quality, outFormat, out)
	}
	if s.imageCache == nil {
		return writeTempFile(ext, write)
	}
	key := derivedImageCacheKey(itemID, kind, index, ref, sourceInfo, plan.W, plan.H, quality, outFormat)
	if f, _, ok := s.imageCache.open(key, ext); ok {
		return f, true
	}
	f, _, ok := s.imageCache.store(key, ext, write)
	if !ok {
		return writeTempFile(ext, write)
	}
	return f, true
}

// writeTempFile makes the picture into a file that lives for one request. The
// directory entry is unlinked as soon as the bytes are written, so the picture
// cannot outlive the request even if the process is killed mid-answer; the open
// handle keeps the bytes readable until the caller closes it.
func writeTempFile(ext string, write func(io.Writer) error) (*os.File, bool) {
	f, err := os.CreateTemp("", "tinyfin-image-*"+ext)
	if err != nil {
		return nil, false
	}
	name := f.Name()
	err = write(f)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return nil, false
	}
	// Unlink now: the open handle keeps the bytes, and the directory entry
	// cannot be left behind by a crash or a killed request.
	_ = os.Remove(name)
	return f, true
}

// writeResizedImage decodes the stored picture, scales it and encodes the
// answer in the format the route picked.
func (s *Server) writeResizedImage(ref storage.ImageRef, plan picture.Plan, quality int, format string, out io.Writer) error {
	f, err := os.Open(ref.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	dst := picture.Resize(src, plan.W, plan.H)
	if format == "png" {
		return png.Encode(out, dst)
	}
	return jpeg.Encode(out, dst, &jpeg.Options{Quality: quality})
}
