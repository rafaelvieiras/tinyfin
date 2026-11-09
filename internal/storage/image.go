package storage

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
