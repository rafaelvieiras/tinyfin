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

// handleChapterImage answers GET /Items/{itemId}/Images/Chapter[/{index}]: the
// picture of the index-th chapter (0 without an index), as it was extracted
// (no resizing). 400 for an index that is not an integer, 404 for an item or
// a chapter without a picture.
func (s *Server) handleChapterImage(w http.ResponseWriter, r *http.Request, itemID storage.ItemID) {
	index := 0
	if v := r.PathValue("imageIndex"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeValidationProblem(w, "imageIndex", "The value '"+v+"' is not valid.")
			return
		}
		index = n
	}
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil || index < 0 || index >= len(item.Chapters) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	path, _, ok := s.chapterimg.Picture(item.ID, item.Chapters[index])
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if tag := r.URL.Query().Get("tag"); tag != "" {
		// the tag is not checked against the picture; it only makes the
		// answer cacheable for good
		w.Header().Set("ETag", `"`+tag+`"`)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public")
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, path)
}

// isChapterImageType reports whether an image type path segment names chapter
// pictures (case-insensitive).
func isChapterImageType(t string) bool { return strings.EqualFold(t, "Chapter") }
