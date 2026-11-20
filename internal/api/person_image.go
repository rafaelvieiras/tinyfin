package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"image"

	"github.com/rafaelvieiras/tinyfin/internal/imagefetch"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A cast/crew credit's photo is downloaded on demand rather than during the
// scan. There are two reasons. The scan can run with no network at all — a
// library mounted read-only on a machine that is offline at night, an NFO
// written by a tool that prefetched thumbs — and a photo nobody looks at costs
// a request the server should not make. The wire result is the same one the
// reference server reaches by downloading during the scan: BaseItemPerson
// carries a PrimaryImageTag, and the item image route serves the bytes
// (docs/13 records that the timing differs).

// personImageRetryAfter is how long a URL that failed to download is left
// alone. Retrying on every request would turn one client with an old NFO into a
// steady stream of outbound requests; not retrying at all would mean a
// transient failure needed a re-scan to fix. A day is long enough to keep a
// dead host quiet and short enough that fixing the URL server-side takes effect
// on its own. Changing the URL retries immediately.
const personImageRetryAfter = 24 * time.Hour

// personPrimaryImageKind is the only image kind a person carries: a portrait,
// nothing else. Same literal as libraryPrimaryImageKind, kept separate so the
// name at each call site says what it is about.
const personPrimaryImageKind = storage.ImageKind("Primary")

// personImageTag is the cache-busting tag a person's photo is published under,
// derived from the URL the photo comes from. It has to exist before the
// download does — the client only asks for an image the DTO named — and it has
// to change when the URL changes, which is what makes a stale cached file
// detectable. 32 hex characters, the width the reference server's own image
// tags have.
func personImageTag(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:16])
}

// personImageFetcher is the download edge, an interface so tests can answer
// without a network. *imagefetch.Fetcher is the production implementation.
type personImageFetcher interface {
	Fetch(ctx context.Context, url string) (imagefetch.Image, error)
}

// personImageFlight is one in-progress download and the result every waiter
// receives.
type personImageFlight struct {
	done chan struct{}
	img  storage.PersonImage
	ok   bool
}

// personImageFlights collapses concurrent downloads of the same person's
// photo. A detail page names a credit once, but the same actor appears on many
// items and a page can be opened twice at the same moment; without this, one
// photo could be downloaded several times over.
type personImageFlights struct {
	mu sync.Mutex
	m  map[storage.ItemID]*personImageFlight
}

// do runs fn unless a download for id is already running, in which case it
// waits for that one's result. Only the leader touches the network or the
// store.
func (f *personImageFlights) do(id storage.ItemID, fn func() (storage.PersonImage, bool)) (storage.PersonImage, bool) {
	f.mu.Lock()
	if f.m == nil {
		f.m = map[storage.ItemID]*personImageFlight{}
	}
	if flight, running := f.m[id]; running {
		f.mu.Unlock()
		<-flight.done
		return flight.img, flight.ok
	}
	flight := &personImageFlight{done: make(chan struct{})}
	f.m[id] = flight
	f.mu.Unlock()

	// The result lands in the struct before done is closed, so a waiter that
	// wakes up reads it without a second lock.
	flight.img, flight.ok = fn()
	f.mu.Lock()
	delete(f.m, id)
	f.mu.Unlock()
	close(flight.done)
	return flight.img, flight.ok
}

// personImageRef resolves a person id to an image the serving path already
// understands, downloading the picture if this is the first request for it.
// ok is false when the id names no person with a photo, or when the photo
// cannot be obtained now — in both cases the route answers 404, which is what
// the client does with an image that is missing.
func (s *Server) personImageRef(ctx context.Context, personID storage.ItemID, kind storage.ImageKind) (storage.ImageRef, bool) {
	// A person has one picture, its portrait; nothing publishes a backdrop or
	// a logo for a credit.
	if kind != personPrimaryImageKind {
		return storage.ImageRef{}, false
	}
	img, ok, err := s.store.GetPersonImage(ctx, personID)
	if err != nil {
		log.Printf("api: querying image of person %q: %v", personID, err)
		return storage.ImageRef{}, false
	}
	if !ok || img.Source == "" {
		return storage.ImageRef{}, false
	}
	// A row without a file is either never downloaded or lost its file
	// out-of-band; both are re-downloaded below rather than served as a 404
	// forever.
	// A local photo replaced on disk is re-read instead of serving the old
	// copy: the source is a file this server can see, and its mtime is the
	// whole change signal.
	if ref, ok := s.cachedPersonImageRef(img); ok && !personImageStale(img) {
		return ref, true
	}
	if s.personImageCoolingDown(img, s.personImageClock()) {
		return storage.ImageRef{}, false
	}
	// Downloading is the slow path, so it is serialized per person and the
	// waiters read the winner's result.
	img, ok = s.personFlights.do(personID, func() (storage.PersonImage, bool) {
		return s.downloadPersonImage(ctx, img)
	})
	if !ok {
		return storage.ImageRef{}, false
	}
	return s.cachedPersonImageRef(img)
}

// personImageServable reports whether a stored person image row can answer a
// request now: the picture is on disk, or the source is one that has not failed
// before. A row whose source failed stays quiet until the retry window passes,
// instead of advertising a tag that answers 404 on every page view.
func (s *Server) personImageServable(img storage.PersonImage) bool {
	if img.Path != "" {
		return true
	}
	return !s.personImageCoolingDown(img, s.personImageClock())
}

// cachedPersonImageRef builds the serving view of an already downloaded
// photo, and reports false when the row has no file on disk.
func (s *Server) cachedPersonImageRef(img storage.PersonImage) (storage.ImageRef, bool) {
	if img.Path == "" {
		return storage.ImageRef{}, false
	}
	info, err := os.Stat(img.Path)
	if err != nil {
		return storage.ImageRef{}, false
	}
	size := img.Size
	if size == 0 {
		size = info.Size()
	}
	tag := img.Tag
	if tag == "" {
		tag = personImageTag(img.Source)
	}
	return storage.ImageRef{
		// The picture is not an image_refs row, so it has no stored ref id;
		// the person id plus the kind names it uniquely.
		ID:       storage.ImageRefID(string(img.PersonID) + "-person-primary"),
		ItemID:   img.PersonID,
		Kind:     personPrimaryImageKind,
		Path:     img.Path,
		Tag:      tag,
		BlurHash: img.BlurHash,
		Width:    img.Width,
		Height:   img.Height,
		Size:     size,
	}, true
}

// personImageCoolingDown reports whether the last attempt at this URL failed
// recently enough that it should not be repeated.
func (s *Server) personImageCoolingDown(img storage.PersonImage, now time.Time) bool {
	if img.FailedAt.IsZero() {
		return false
	}
	return now.Sub(img.FailedAt) < personImageRetryAfter
}

// downloadPersonImage fetches and stores one person's photo. Every outcome is
// written back: success records the file, the tag, the dimensions and the
// blurhash; failure records only when, so the next request can tell "not
// downloaded" from "known bad". A failure is never an error page — the caller
// turns it into a 404 and the page around it keeps rendering.
func (s *Server) downloadPersonImage(ctx context.Context, img storage.PersonImage) (storage.PersonImage, bool) {
	if s.cfg.ScanImageDir == "" {
		// No place to put the bytes: the feature is off, exactly like the
		// scanner's provider artwork (Config.ScanImageDir).
		return storage.PersonImage{}, false
	}
	fetched, err := s.personImageFetcher.Fetch(ctx, img.Source)
	if err != nil {
		s.recordPersonImageFailure(ctx, img, err)
		return storage.PersonImage{}, false
	}
	path := filepath.Join(s.cfg.ScanImageDir, string(img.PersonID)+"-Primary"+personImageExt(fetched))
	if err := os.WriteFile(path, fetched.Bytes, 0o644); err != nil {
		s.recordPersonImageFailure(ctx, img, err)
		return storage.PersonImage{}, false
	}
	width, height := imageDimensionsOf(path)
	img.Path = path
	img.Tag = personImageTagOf(img.Source, fetched.Modified)
	img.FailedAt = time.Time{}
	img.Width, img.Height = width, height
	img.Size = int64(len(fetched.Bytes))
	// Computed once, here, from the file just written: the DTO publishes the
	// placeholder on every detail response, and decoding the picture again on
	// each of those would be wasteful.
	img.BlurHash = scan.ImageFileBlurHash(path)
	if err := s.store.UpsertPersonImage(ctx, img); err != nil {
		log.Printf("api: storing image of person %q: %v", img.PersonID, err)
		return storage.PersonImage{}, false
	}
	return img, true
}

// recordPersonImageFailure stores when a download failed. The cached picture
// fields are cleared: whatever was there belonged to a URL that no longer
// answers.
func (s *Server) recordPersonImageFailure(ctx context.Context, img storage.PersonImage, cause error) {
	log.Printf("api: downloading image of person %q from %s: %v", img.PersonID, img.Source, cause)
	img.Path, img.Tag, img.BlurHash = "", "", ""
	img.Width, img.Height, img.Size = 0, 0, 0
	img.FailedAt = s.personImageClock()
	if err := s.store.UpsertPersonImage(ctx, img); err != nil {
		log.Printf("api: recording the failed image download of person %q: %v", img.PersonID, err)
	}
}

// personImagesFor loads the stored photos of an item's credits in one query, so
// a detail response does not ask the store once per actor. A lookup error
// leaves the tags off rather than failing the response, the same "a secondary
// field never breaks the item" posture the rest of the DTO takes.
func (s *Server) personImagesFor(ctx context.Context, people []storage.Person) map[storage.ItemID]storage.PersonImage {
	if len(people) == 0 {
		return nil
	}
	ids := make([]storage.ItemID, 0, len(people))
	for _, p := range people {
		ids = append(ids, storage.ItemID(personID(p.Name)))
	}
	images, err := s.store.QueryPersonImages(ctx, ids)
	if err != nil {
		log.Printf("api: querying images of %d credits: %v", len(ids), err)
		return nil
	}
	return images
}

// personImageExt picks the file extension for a downloaded photo from what it
// actually is, so a PNG does not end up named .jpg and served with a type it
// does not have.
func personImageExt(img imagefetch.Image) string {
	if exts, err := mime.ExtensionsByType(img.MIME); err == nil && len(exts) > 0 {
		// ExtensionsByType answers in a fixed order; .jpe for JPEG is the
		// odd one out of that list.
		for _, ext := range exts {
			if ext != ".jpe" {
				return ext
			}
		}
	}
	switch strings.TrimPrefix(img.MIME, "image/") {
	case "png":
		return ".png"
	case "webp":
		return ".webp"
	case "gif":
		return ".gif"
	}
	return ".jpg"
}

// imageDimensionsOf reads an image file's pixel dimensions; 0,0 on any decode
// failure, which the DTO treats as "unknown" rather than as an error.
func imageDimensionsOf(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// personImageTagOf is the tag a person's picture is published under. For a
// download it is the source URL's hash, so a change of URL changes the tag. For
// a file on this server the path alone would not change when the picture is
// replaced, so the file's own modification time joins the hash.
func personImageTagOf(source string, modified time.Time) string {
	if modified.IsZero() {
		return personImageTag(source)
	}
	return personImageTag(source + "|" + strconv.FormatInt(modified.UnixNano(), 10))
}

// personImageStale reports whether a local source file is newer than the copy
// taken from it.
func personImageStale(img storage.PersonImage) bool {
	if img.Path == "" || !filepath.IsAbs(img.Source) {
		return false
	}
	src, err := os.Stat(img.Source)
	if err != nil {
		return false
	}
	stored, err := os.Stat(img.Path)
	if err != nil {
		return false
	}
	return src.ModTime().After(stored.ModTime())
}
