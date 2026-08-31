package storage

import "time"

// CollectionType is the kind of content a library holds — matches the
// reference server's CollectionTypeOptions, narrowed to the content types
// docs/01 puts in v1 scope (video-first: movies/series, plus music and
// mixed collections). Unlike ItemType, this is a small closed set at the
// reference server itself (an explicit enum, not an open wire vocabulary),
// so it's reasonable to enumerate it in full for the kinds Tinyfin
// supports rather than leaving it a bare string.
type CollectionType string

const (
	CollectionTypeMovies  CollectionType = "movies"
	CollectionTypeTVShows CollectionType = "tvshows"
	CollectionTypeMusic   CollectionType = "music"
	CollectionTypeBoxSets CollectionType = "boxsets"
	CollectionTypeMixed   CollectionType = "mixed"
)

// Library is the storage-side counterpart of the reference server's
// VirtualFolderInfo (Name, Locations, CollectionType) plus the scan
// bookkeeping LibraryStore's doc comment calls for — what the scanner (08)
// last saw, so it can decide what changed on the next pass.
type Library struct {
	ID             LibraryID
	Name           string
	CollectionType CollectionType

	// Paths are the filesystem locations backing this library —
	// VirtualFolderInfo.Locations. A library can span more than one path
	// (the reference server allows adding several folders to one library).
	Paths []string

	// LastScannedAt is nil until the scanner (08) has completed at least
	// one pass over this library.
	LastScannedAt *time.Time
}
