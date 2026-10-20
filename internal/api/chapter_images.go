// chapter_images.go serves the pictures of chapter marks
// (docs/contract/specs/chapters.md): GET /Items/{itemId}/Images/Chapter/{index}
// and the ImageTag/ImageDateModified of Chapters on an item. The pictures
// are extracted by internal/chapterimg.
package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/chapterimg"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// chapterDtos lists an item's chapters; a chapter without a title is named
// "Chapter N" after its 1-based position, and one with a picture carries its
// ImageTag and date. The result is never nil: a read of an item without
// chapters carries an empty list.
func (s *Server) chapterDtos(item storage.Item) []chapterInfoDto {
	out := make([]chapterInfoDto, 0, len(item.Chapters))
	for i, c := range item.Chapters {
		name := c.Name
		if name == "" {
			name = "Chapter " + strconv.Itoa(i+1)
		}
		d := chapterInfoDto{StartPositionTicks: c.StartTicks, Name: name}
		if path, mod, ok := s.chapterimg.Picture(item.ID, c); ok {
			d.ImageTag = chapterimg.Tag(path, mod)
			d.ImageDateModified = wireTime{mod}
		}
		out = append(out, d)
	}
	return out
}

// handleChapterImage answers GET /Items/{itemId}/Images/Chapter[/{index}] and
// its long form with the tag, format, size and overlay values in the path: the
// picture of the index-th chapter (0 without an index). The stored still is
// served as it is unless the query asks for a change (size, quality, format;
// see chapterimg.PlanFor); the picture is never enlarged. 400 for a value
// that is not a number (every bad one is listed), 404 for an item or a
// chapter without a picture.
func (s *Server) handleChapterImage(w http.ResponseWriter, r *http.Request, itemID storage.ItemID) {
	q := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			q[strings.ToLower(k)] = strings.TrimSpace(v[0])
		}
	}
	// the long form's path members win over the query
	for _, pv := range [][2]string{{"tag", "tag"}, {"format", "format"}, {"maxWidth", "maxwidth"}, {"maxHeight", "maxheight"}, {"percentPlayed", "percentplayed"}, {"unplayedCount", "unplayedcount"}} {
		if v := r.PathValue(pv[0]); v != "" {
			q[pv[1]] = v
		}
	}
	errs := map[string][]string{}
	bad := func(name, v string) { errs[name] = []string{"The value '" + v + "' is not valid."} }
	var p chapterimg.Params
	for _, f := range []struct {
		name string
		dst  **int
	}{{"maxWidth", &p.MaxWidth}, {"maxHeight", &p.MaxHeight}, {"width", &p.Width}, {"height", &p.Height},
		{"fillWidth", &p.FillWidth}, {"fillHeight", &p.FillHeight}, {"quality", &p.Quality}} {
		v, given, ok := chapterimg.ParseInt(q[strings.ToLower(f.name)])
		if given && !ok {
			bad(f.name, q[strings.ToLower(f.name)])
			continue
		}
		*f.dst = v
	}
	for _, f := range []struct {
		name string
		dst  **int
	}{{"blur", &p.Blur}, {"unplayedCount", &p.UnplayedCount}} {
		if v := q[strings.ToLower(f.name)]; v != "" {
			n, _, ok := chapterimg.ParseInt(v)
			if !ok {
				bad(f.name, v)
				continue
			}
			*f.dst = n
		}
	}
	if v := q["percentplayed"]; v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			bad("percentPlayed", v)
		} else {
			p.PercentPlayed = &f
		}
	}
	p.BackgroundColor = q["backgroundcolor"]
	p.ForegroundLayer = q["foregroundlayer"]
	p.Webp = acceptsWebP(r.Header.Get("Accept"))
	p.Format = q["format"]
	if r.PathValue("format") != "" && !isImageFormatName(p.Format) {
		bad("format", p.Format)
	}
	index := 0
	if v := r.PathValue("imageIndex"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			bad("imageIndex", v)
		}
		index = n
	}
	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	if !chapterimg.ValidFormat(p.Format) {
		// a format number out of range is not a binding error
		http.Error(w, "Error processing request.", http.StatusBadRequest)
		return
	}
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil || index < 0 || index >= len(item.Chapters) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	path, mod, ok := s.chapterimg.Picture(item.ID, item.Chapters[index])
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	file, ctype, err := s.chapterimg.Render(item.ID, path, mod, p)
	if err != nil {
		file, ctype = path, "image/jpeg"
	}
	if tag := q["tag"]; tag != "" {
		// the tag is not checked against the picture; it only makes the
		// answer cacheable for good
		w.Header().Set("ETag", `"`+tag+`"`)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public")
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Vary", "Accept")
	// pictures are always sent whole
	r.Header.Del("Range")
	http.ServeFile(w, r, file)
}

// isImageFormatName reports whether s names an image format (Bmp, Gif, Jpg,
// Png, Webp, Svg in any case, or its number 0 to 5).
func isImageFormatName(s string) bool {
	switch strings.ToLower(s) {
	case "bmp", "gif", "jpg", "png", "webp", "svg", "0", "1", "2", "3", "4", "5":
		return true
	}
	return false
}

// isChapterImageType reports whether an image type path segment names chapter
// pictures (case-insensitive).
func isChapterImageType(t string) bool { return strings.EqualFold(t, "Chapter") }

// acceptsWebP reports whether an Accept header lists image/webp as a media
// type of its own (any parameters, q included, are not looked at; the match
// is case-sensitive).
func acceptsWebP(h string) bool {
	for _, part := range strings.Split(h, ",") {
		mt, _, _ := strings.Cut(part, ";")
		if strings.TrimSpace(mt) == "image/webp" {
			return true
		}
	}
	return false
}
