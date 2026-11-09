// library_image.go implements the picture a media library itself carries: the
// image a dashboard draws on the library's card.
//
// The reference server publishes one for every library it can build one for —
// GET /Library/VirtualFolders reports PrimaryImageItemId = the library's own
// ItemId, and the image is served from /Items/{libraryItemId}/Images/Primary
// like any item image (observed live: a 960x540 PNG stored under the server's
// metadata tree, stable across scans, resizable through the usual
// maxWidth/quality query params). jellyfin-web's libraries page only renders
// the card image when PrimaryImageItemId is present, falling back to the
// collection-type icon otherwise.
//
// Tinyfin generates its own when the library has none: a mosaic of up to four
// of the library's posters (internal/libraryart) written into the scan image
// directory, which is also where an admin upload lands. Libraries with no
// artwork of their own get none — same rule as the reference, where a library
// with no media posters gets no generated image.
package api

import (
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rafaelvieiras/tinyfin/internal/libraryart"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// libraryPrimaryImageKind is the only image kind a library carries today.
// Written as a literal because storage's image kinds are plain strings with no
// per-kind constants (api/dto.go's singleInstanceImageKinds list is the same
// shape).
const libraryPrimaryImageKind = storage.ImageKind("Primary")

// libraryPrimaryImageRef adapts a stored library image to the ImageRef the
// image-serving path already understands, so the bytes, the ETag, the
// Last-Modified and the image cache all work unchanged.
func libraryPrimaryImageRef(img storage.LibraryImage) storage.ImageRef {
	return storage.ImageRef{
		ID:     storage.ImageRefID(string(img.LibraryID) + "-library-primary"),
		ItemID: storage.ItemID(img.LibraryID),
		Kind:   img.Kind,
		Path:   img.Path,
		Tag:    img.Tag,
		Width:  img.Width,
		Height: img.Height,
		Size:   img.Size,
	}
}

// libraryHasPrimaryImage reports whether lib has a card image, by the rule the
// client applies: PrimaryImageItemId present means "draw the image".
func (s *Server) libraryHasPrimaryImage(ctx context.Context, libID storage.LibraryID) (bool, error) {
	_, ok, err := s.store.GetLibraryImage(ctx, libID, libraryPrimaryImageKind)
	return ok, err
}

// ensureLibraryImage generates the library's card image when it has none. It
// runs after a successful scan, which is when the library's posters are known
// to be cataloged; a library that already has one — generated earlier or
// uploaded by an admin — is left alone, so the picture stays stable across
// scans the way the reference server's does.
func (s *Server) ensureLibraryImage(ctx context.Context, lib storage.Library) {
	if s.cfg.ScanImageDir == "" {
		return
	}
	if ok, err := s.libraryHasPrimaryImage(ctx, lib.ID); err != nil || ok {
		if err != nil {
			log.Printf("api: checking image of library %q: %v", lib.Name, err)
		}
		return
	}

	sources := s.libraryArtSources(ctx, lib)
	if len(sources) == 0 {
		return
	}

	path := filepath.Join(s.cfg.ScanImageDir, string(lib.ID)+"-libraryart.png")
	f, err := os.Create(path)
	if err != nil {
		log.Printf("api: creating library image file for %q: %v", lib.Name, err)
		return
	}
	err = libraryart.Compose(f, lib.Name, sources)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		log.Printf("api: composing image for library %q: %v", lib.Name, err)
		return
	}

	fi, err := os.Stat(path)
	if err != nil {
		log.Printf("api: statting image of library %q: %v", lib.Name, err)
		return
	}
	img := storage.LibraryImage{
		LibraryID: lib.ID,
		Kind:      libraryPrimaryImageKind,
		Path:      path,
		// A fresh tag per generated file, same cache-busting rule
		// handleUploadItemImage documents.
		Tag: randomID(),
		// The reference publishes a blurhash for a library's card image like
		// any other item image (GET /UserViews carries ImageBlurHashes), and
		// jellyfin-web paints it while the real picture loads. Same encoder
		// the scanner uses for item artwork.
		BlurHash: scan.ImageFileBlurHash(path),
		Width:    libraryart.Width,
		Height:   libraryart.Height,
		Size:     fi.Size(),
	}
	if err := s.store.UpsertLibraryImage(ctx, img); err != nil {
		log.Printf("api: storing image of library %q: %v", lib.Name, err)
	}
}

// libraryArtSources decodes up to libraryart's band count of the library's own
// pictures, most recently added first, preferring a primary image (a poster)
// and falling back to a backdrop when that is all an item has. Items whose
// file cannot be decoded are skipped rather than failing the composition.
func (s *Server) libraryArtSources(ctx context.Context, lib storage.Library) []image.Image {
	const candidatePool = 24 // read this many items to find up to MaxSources pictures

	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		LibraryID: &lib.ID,
		SortBy:    []storage.SortField{{By: storage.SortByDateCreated, Descending: true}},
		Limit:     candidatePool,
	})
	if err != nil {
		log.Printf("api: listing items of library %q for its image: %v", lib.Name, err)
		return nil
	}

	var out []image.Image
	for _, item := range page.Items {
		if len(out) >= libraryart.MaxSources {
			break
		}
		for _, kind := range []storage.ImageKind{libraryPrimaryImageKind, storage.ImageKind("Backdrop")} {
			refs, err := s.store.GetImageRefs(ctx, item.ID, kind)
			if err != nil || len(refs) == 0 || refs[0].Path == "" {
				continue
			}
			img, err := decodeImageFile(refs[0].Path)
			if err != nil {
				continue
			}
			out = append(out, img)
			break
		}
	}
	return out
}

// decodeImageFile reads path and decodes it as an image, reporting a decode or
// read failure to the caller (which skips that picture) instead of failing
// anything larger.
func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return img, nil
}

// libraryForItemID resolves an id the images routes received to a library, if
// it names one — the same dual meaning POST /Items/{itemId}/Refresh already
// has for a library id (item_refresh.go's package comment).
func (s *Server) libraryForItemID(ctx context.Context, itemID storage.ItemID) (storage.Library, bool) {
	lib, err := s.store.GetLibrary(ctx, storage.LibraryID(itemID))
	return lib, err == nil
}

// uploadLibraryImage stores an admin upload as the library's card image. Only
// Primary is accepted: a library carries no other kind today, and a request for
// one is answered 204 without storing anything rather than inventing a
// behavior the reference was never observed to have.
func (s *Server) uploadLibraryImage(w http.ResponseWriter, r *http.Request, lib storage.Library, kind storage.ImageKind, data []byte) {
	if kind != libraryPrimaryImageKind || s.cfg.ScanImageDir == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ext := extensionForContentType(r.Header.Get("Content-Type"))
	path := filepath.Join(s.cfg.ScanImageDir, string(lib.ID)+"-library"+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	previous, hadPrevious, _ := s.store.GetLibraryImage(r.Context(), lib.ID, kind)
	width, height := imageDimensions(data)
	img := storage.LibraryImage{
		LibraryID: lib.ID,
		Kind:      kind,
		Path:      path,
		// Fresh tag per upload, same cache-busting rule as an item upload
		// (handleUploadItemImage).
		Tag:      randomID(),
		BlurHash: scan.ImageFileBlurHash(path),
		Width:    width,
		Height:   height,
		Size:     int64(len(data)),
	}
	if err := s.store.UpsertLibraryImage(r.Context(), img); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if hadPrevious && previous.Path != "" && previous.Path != path {
		// The replaced file (a previous upload, or a generated mosaic) would
		// otherwise linger in the image directory forever.
		_ = os.Remove(previous.Path)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteLibraryImage removes the library's image. Deleting is idempotent (204
// either way, like an item's own image delete): a library that never had one,
// or an index that names nothing, is not an error. The next scan of the library
// composes a fresh mosaic unless an admin uploads one first — a documented
// consequence of generating on "no image", not a hidden side effect
// (docs/11).
func (s *Server) deleteLibraryImage(w http.ResponseWriter, r *http.Request, lib storage.Library, kind storage.ImageKind, index int) {
	if index != 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	img, ok, err := s.store.GetLibraryImage(r.Context(), lib.ID, kind)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if ok {
		if img.Path != "" {
			_ = os.Remove(img.Path)
		}
		if err := s.store.DeleteLibraryImage(r.Context(), lib.ID, kind); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
