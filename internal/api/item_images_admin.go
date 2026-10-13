// item_images_admin.go implements the dashboard's "Edit images" flow for a
// single item: list every image an item has (with metadata, not bytes),
// upload a new one, and delete one. All three are admin-only, matching the
// reference server (confirmed live against the reference server:
// GET .../Images requires a valid token; unlike handleItemImage's byte-serving
// GET, which is deliberately unauthenticated for <img src> compatibility —
// see that handler's doc comment in library.go — this metadata/management
// surface isn't rendered by a bare <img> tag, so there's no equivalent
// reason to relax auth here).
package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// imageInfo mirrors the reference server's ImageInfo shape for
// GET /Items/{itemId}/Images, confirmed live against the reference server
// (reference server): a flat list, one entry per existing image, fields
// ImageType/ImageTag/Path/BlurHash/Height/Width/Size always present, and
// ImageIndex present only on kinds that support more than one image
// (Backdrop) — omitted entirely for single-instance kinds rather than sent
// as 0, hence the pointer.
type imageInfo struct {
	ImageType  string `json:"ImageType"`
	ImageIndex *int   `json:"ImageIndex,omitempty"`
	ImageTag   string `json:"ImageTag"`
	Path       string `json:"Path"`
	BlurHash   string `json:"BlurHash"`
	Height     int    `json:"Height"`
	Width      int    `json:"Width"`
	Size       int64  `json:"Size"`
}

// allImageKinds is every storage.ImageKind an item can have an ImageRef
// for — singleInstanceImageKinds (dto.go) plus Backdrop, the one kind that
// isn't in that list because it's multi-instance (dto.go's doc comment on
// singleInstanceImageKinds explains why). Trickplay/Chapter images are
// deliberately excluded: they aren't image_refs/ImageStore records at all
// (separate stores — same dto.go comment).
var allImageKinds = append(append([]storage.ImageKind{}, singleInstanceImageKinds...), storage.ImageKind("Backdrop"))

// isMultiInstanceKind reports whether kind is one an item can have more
// than one of (only Backdrop today) — the same distinction
// singleInstanceImageKinds encodes, inverted.
func isMultiInstanceKind(kind storage.ImageKind) bool {
	return !slices.Contains(singleInstanceImageKinds, kind)
}

// handleListItemImages serves GET /Items/{itemId}/Images — every image
// metadata entry for the item, across every known kind. An item with no
// images at all gets an empty list with 200, not 404 — same "honest empty"
// convention as handleStudios/handleItemSimilar (library.go): the
// dashboard's "Edit images" panel calls this unconditionally when it opens,
// before any image has ever been added, and a 404 there reads as a broken
// item rather than "nothing yet."
func (s *Server) handleListItemImages(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))

	out := []imageInfo{}
	for _, kind := range allImageKinds {
		refs, err := s.store.GetImageRefs(r.Context(), itemID, kind)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		multi := isMultiInstanceKind(kind)
		for _, ref := range refs {
			info := imageInfo{
				ImageType: string(ref.Kind),
				ImageTag:  ref.Tag,
				Path:      ref.Path,
				BlurHash:  ref.BlurHash,
				Height:    ref.Height,
				Width:     ref.Width,
				Size:      ref.Size,
			}
			if multi {
				idx := ref.Index
				info.ImageIndex = &idx
			}
			out = append(out, info)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUploadItemImage serves POST /Items/{itemId}/Images/{imageType} and
// .../Images/{imageType}/{imageIndex}. The body is the raw image bytes
// base64-encoded, not multipart (confirmed live against
// the reference server: a base64 body with Content-Type: image/png
// succeeds with 204; the same bytes sent un-encoded 500s) — Content-Type
// otherwise only informs the extension the file is saved under here.
//
// For a multi-instance kind (Backdrop), an explicit imageIndex that names
// an existing image overwrites it in place; otherwise the upload appends
// as a new image at the next index, ignoring an imageIndex that doesn't
// match anything existing — matching what was observed live (a POST to
// .../Backdrop/5 against an item with one existing Backdrop landed as
// index 1, i.e. "next", not "5"). For a single-instance kind, there is at
// most ever one image regardless of what imageIndex says, so a re-upload
// always overwrites it.
func (s *Server) handleUploadItemImage(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	kind := storage.ImageKind(r.PathValue("imageType"))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Reference server's own body is base64-only (see doc comment above),
	// so a decode failure is treated the same way it was observed to
	// respond: a hard failure, not a lenient "treat as raw bytes" fallback.
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if s.cfg.ScanImageDir == "" {
		// No directory configured to persist uploaded artwork into — same
		// "nowhere to write" gap storeProviderImage (scan.go) silently
		// skips for provider downloads, but an explicit admin upload
		// request has no silent-skip option: the client expects either the
		// image to exist afterward or an error, never a false 204.
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	existing, err := s.store.GetImageRefs(r.Context(), itemID, kind)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ref := storage.ImageRef{ItemID: itemID, Kind: kind}
	if isMultiInstanceKind(kind) {
		requested := -1
		if v := r.PathValue("imageIndex"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				requested = n
			}
		}
		ref.Index = len(existing) // default: append as the next image
		for _, e := range existing {
			if e.Index == requested {
				ref = e // overwrite the named image in place
				break
			}
		}
	} else if len(existing) > 0 {
		ref = existing[0] // single-instance kind: always overwrite the one ref
	}
	if ref.ID == "" {
		ref.ID = storage.ImageRefID(randomID())
	}

	ext := extensionForContentType(r.Header.Get("Content-Type"))
	path := filepath.Join(s.cfg.ScanImageDir, fmt.Sprintf("%s-%s-%d%s", itemID, kind, ref.Index, ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	width, height := imageDimensions(data)
	ref.Path = path
	ref.Size = int64(len(data))
	ref.Width = width
	ref.Height = height
	// A fresh Tag on every upload — the cache-busting value ImageRef.Tag's
	// doc comment (storage/image.go) describes; without one changing here,
	// a re-uploaded image would keep serving the old ETag/If-None-Match
	// value and a client could keep the stale cached bytes.
	ref.Tag = randomID()

	if err := s.store.UpsertImageRef(r.Context(), ref); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteItemImage serves DELETE /Items/{itemId}/Images/{imageType}
// and .../Images/{imageType}/{imageIndex} — same index-matching search as
// handleItemImage (library.go). Deleting an index with no matching ref is
// 204, not 404 — confirmed live against the reference server: DELETE on a
// nonexistent index there responds 204 idempotently, so a client retrying a
// delete (or racing another admin) never sees a spurious failure.
func (s *Server) handleDeleteItemImage(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	kind := storage.ImageKind(r.PathValue("imageType"))

	index := 0
	if v := r.PathValue("imageIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			index = n
		}
	}

	refs, err := s.store.GetImageRefs(r.Context(), itemID, kind)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, ref := range refs {
		if ref.Index != index {
			continue
		}
		if ref.Path != "" {
			// Ignore the error: an already-missing file (removed
			// out-of-band) isn't a reason to fail the ref delete — same
			// "expected gap, not a crash" convention handleItemImage's
			// os.Stat check documents for the read side.
			_ = os.Remove(ref.Path)
		}
		if err := s.store.DeleteImageRef(r.Context(), ref.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// extensionForContentType picks a file extension from an upload's
// Content-Type header — confirmed live that the reference server does the
// same (a Content-Type: image/png upload was persisted server-side under a
// .png path, not .jpg). Defaults to .jpg, matching storeProviderImage's
// (scan.go) own hardcoded default for the same "always has to save
// *something*" reason.
func extensionForContentType(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.TrimSpace(strings.ToLower(mediaType)) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	default:
		return ".jpg"
	}
}

// imageDimensions decodes just enough of data's header to get its pixel
// dimensions — same approach and same "0,0 on failure isn't fatal"
// convention as scan.go's imageDimensions (a separate copy: that one isn't
// exported, and this package must not import internal/scan for it).
func imageDimensions(data []byte) (width, height int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
