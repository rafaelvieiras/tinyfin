package storage

import "time"

// ImageRef is one entry in the list ImageStore.GetImageRefs returns for a
// given (itemID, kind). ID is its storage identity (see ImageStore in
// store.go for why this is ID-addressed, not positional). ItemID and Kind
// are carried on the struct itself, not just passed as GetImageRefs
// params, because UpsertImageRef/DeleteImageRef take only a ref/ID — the
// ref has to be self-describing for those to work. Remaining fields
// mirror the reference server's ImageInfo (04): Path is the ref-not-blob
// pointer to the actual file (see ImageStore's doc comment on why bytes
// aren't stored here); Tag is the cache-busting value the reference
// server's ImageTag serves as (clients use it in image URLs and If-None-Match).
type ImageRef struct {
	ID     ImageRefID
	ItemID ItemID
	Kind   ImageKind

	// Index distinguishes multiple images of the same kind on one item
	// (e.g. backdrop 0, 1, 2, ...) — ImageInfo.ImageIndex.
	Index int

	Path     string
	Tag      string
	BlurHash string
	Width    int
	Height   int
	Size     int64
}

// TrickplayInfo is one resolution's worth of seek-thumbnail sprite-sheet
// metadata for an item — grid layout and interval, not image bytes.
// Shape mirrors the reference server's public TrickplayInfoDto exactly:
// Width/Height are one tile's dimensions, TileWidth/TileHeight are the
// sprite sheet's grid size in tiles, ThumbnailCount is the total non-black
// tile count, Interval is the seek-thumbnail spacing in milliseconds, and
// Bandwidth is the peak bits-per-second the sprite sheet delivery costs.
// Width is also the natural key TrickplayStore.DeleteTrickplayInfo takes
// (one item can have more than one resolution's worth over time; no
// dedicated ID is needed since a resolution can only exist once per item).
type TrickplayInfo struct {
	Width          int
	Height         int
	TileWidth      int
	TileHeight     int
	ThumbnailCount int
	Interval       int
	Bandwidth      int
}

// LibraryImage is the picture a media library shows on a dashboard card
// (LibraryImageStore's doc comment in store.go). Same fields ImageRef carries,
// minus the per-item identity: the library id and the kind are the key, and
// there is only ever one image per kind.
type LibraryImage struct {
	LibraryID LibraryID
	Kind      ImageKind

	Path     string
	Tag      string
	BlurHash string
	Width    int
	Height   int
	Size     int64
}

// PersonImage is the cached photo of one cast/crew credit — a person's own
// picture, not any item's artwork (Person.ImageURL is where it comes from).
//
// It is a table of its own for the reason LibraryImage is: ImageRef hangs off
// items(id) with a foreign key, and a person is not a catalog item here — the
// person exists only as a name carried by items (storage.NamedID), with no
// items row to attach a picture to. The person id is the key, and one row
// holds both halves of the feature:
//
//   - Source, the URL the picture is published at, written by a scan from the
//     item's credits and served as BaseItemPerson.PrimaryImageTag's input. It
//     is what lets the DTO advertise an image before any byte is downloaded,
//     which is the only way an on-demand download can ever start: the client
//     asks for what the DTO names.
//   - the download cache (Path/Tag/BlurHash/Width/Height/Size), filled the
//     first time a client requests the picture, plus FailedAt, the negative
//     cache that keeps a URL which does not answer from being retried on every
//     request.
//
// Tag is derived from Source, not from the downloaded bytes: it has to be
// stable before the download exists, and it has to change when Source changes,
// which is also what makes a stale Path detectable.
type PersonImage struct {
	PersonID ItemID

	// Source is the absolute URL the photo is published at, empty when the
	// credit has none.
	Source string

	Path     string
	Tag      string
	BlurHash string
	Width    int
	Height   int
	Size     int64

	// FailedAt is when a download of Source last failed. Zero means no
	// failure is recorded. A later attempt is allowed once it is older than
	// the retry interval, and immediately when Source changes.
	FailedAt time.Time
}
