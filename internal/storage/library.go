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
	// CollectionTypePlaylists is the hidden library that holds playlists.
	CollectionTypePlaylists CollectionType = "playlists"
	CollectionTypeMixed     CollectionType = "mixed"
	// CollectionTypeHomeVideos holds loose video files in any folder
	// structure: no naming scheme, no series or movie identification.
	CollectionTypeHomeVideos CollectionType = "homevideos"
	// CollectionTypeMusicVideos holds music clips in any folder structure.
	CollectionTypeMusicVideos CollectionType = "musicvideos"
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

	// LibraryOptions is the reference server's LibraryOptions object for
	// this library (metadata providers, trickplay, etc. — ~35 fields),
	// persisted opaquely as the raw JSON the admin dashboard sent to POST
	// /Library/VirtualFolders/LibraryOptions. Tinyfin doesn't act on any of
	// those fields yet (a documented gap — see internal/api/library_admin.go),
	// but round-trips them honestly: save, then GET /Library/VirtualFolders
	// echoes back exactly this blob. nil/empty means "never set."
	LibraryOptions []byte
}
