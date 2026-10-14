package storage

import (
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/rating"
)

// Identifier types referenced throughout the Store interfaces (docs/05,
// O3). All are string-backed: SQLite mode stores them as TEXT primary
// keys, and a networked scale-mode implementation is free to use UUIDs or
// any other string encoding without changing this interface.
type (
	ItemID         string
	UserID         string
	DeviceID       string
	LibraryID      string
	SubtitleID     string
	LyricID        string
	ItemRelationID string
	ImageRefID     string

	// ImageKind identifies the role an image plays on an item — e.g.
	// "primary", "backdrop", "logo" — matching the reference server's
	// ImageType values (04's ImageInfo.ImageType). Left as a plain string
	// rather than a closed Go enum for the same reason ItemRelation.Kind
	// is: it is a wire vocabulary the reference server can extend without
	// requiring an interface change here.
	ImageKind string
)

// ItemRelation is a typed, ordered link between two items. ID is the
// relation row's own identity — required because the same (parent,
// child, kind) can legitimately repeat, e.g. the reference server's
// PlaylistItemId shows one item can appear more than once in the same
// playlist. Kind names are implementation-defined strings, not a closed
// Go enum, so a new relation kind doesn't require an interface change —
// e.g. "parent" mirrors what Item.ParentID already gives for the tree
// case, "collection", "playlist", "credit".
type ItemRelation struct {
	ID        ItemRelationID
	ParentID  ItemID
	ChildID   ItemID
	Kind      string
	SortIndex int
}

// ItemFilter drives every catalog read. Limit is required — implementations
// reject Limit <= 0 rather than defaulting to "return everything".
//
// UserID/Played/Favorite are here (not left to a separate playstate query)
// because playstate-aware browsing — Continue Watching, Next Up, Favorites
// — is core "library and item navigation" (04's highest-priority group),
// not an admin add-on. QueryItems is free to join PlaystateStore
// internally to answer them; that join never crosses the Store boundary
// back out to the caller.
type ItemFilter struct {
	LibraryID *LibraryID
	ParentID  *ItemID
	// SeriesID matches Item.SeriesID directly (set on both Season and
	// Episode rows, item.go's doc comment) rather than walking ParentID —
	// backs GET /Shows/{seriesId}/Episodes when called without a SeasonId
	// (docs/04's Specialized media types group), which needs every episode
	// across every season of a series in one query, not just one season's
	// direct children the way ParentID alone would give.
	SeriesID *ItemID
	// Path matches Item.Path exactly — backs the scanner's rescan-reconciles-
	// by-path lookup (internal/scan/scan.go's existingItemID): before
	// minting a fresh deterministic ID for a movie/episode, the scanner
	// checks whether an item already exists at that path (LibraryID+Path)
	// and reuses its ID instead, so a file cataloged under an older or
	// differently-derived ID (a prior devseed run, a since-changed hashing
	// scheme, ...) gets updated in place on the next scan rather than
	// duplicated alongside a second row for the same file.
	Path       string
	ItemTypes  []ItemType
	Genres     []string
	SearchText string // backs FTS5 search, 09
	Recursive  bool
	UserID     *UserID
	Played     *bool
	Favorite   *bool
	// Resume selects items with a saved, unfinished playback position for
	// UserID (PlaybackPositionTicks > 0 and not Played) — backs GET
	// /UserItems/Resume ("Continue Watching"). Requires UserID; ignored
	// otherwise, same as Played/Favorite above. With no SortBy the result is
	// ordered by last played date, most recent first.
	Resume bool
	SortBy []SortField
	Limit  int
	Offset int

	// Listing/query filters (GET /Items). Every field is optional and
	// ANDed with the rest; a zero value means "no restriction".

	// RestrictToLibraries limits results to LibraryIDs. It is a separate
	// flag because an empty LibraryIDs must mean "no accessible library,
	// match nothing" for a restricted user, not "unrestricted".
	RestrictToLibraries bool
	LibraryIDs          []LibraryID

	// TopLevelOnly keeps only the direct children of a library root:
	// items with no parent, excluding the library's own root Folder item.
	TopLevelOnly bool
	// RootFolderPaths are the paths of the library's own root Folder items.
	// With TopLevelOnly they are the only parentless Folder items left out;
	// a parentless Folder anywhere else (a first-level folder of a
	// home-videos library) is a direct child. Empty: every parentless
	// Folder is left out.
	RootFolderPaths []string

	// IDs selects specific items; OrderByIDs returns them in that order
	// when no explicit SortBy is set. ExcludeIDs removes items.
	IDs        []ItemID
	OrderByIDs bool
	ExcludeIDs []ItemID

	ExcludeItemTypes []ItemType
	// IsFolder keeps only folder-like items (true) or only leaves (false).
	IsFolder *bool

	Years                   []int
	NameStartsWith          string
	NameStartsWithOrGreater string
	NameLessThan            string
	// NameContains is a case-insensitive substring match on the item's name
	// (and, for items of a series, the series' name). When set and no
	// SortBy is given, results are ranked by match quality, then sort name.
	NameContains string

	ParentIndexNumber *int
	IndexNumber       *int

	// Video dimension filters, evaluated against the primary video stream.
	MinWidth, MinHeight, MaxWidth, MaxHeight *int
	IsHD                                     *bool
	Is4K                                     *bool

	Tags               []string
	OfficialRatings    []string
	PersonName         string
	HasOverview        *bool
	HasSubtitles       *bool
	HasImdbID          *bool
	HasTmdbID          *bool
	HasParentalRating  *bool
	MinCommunityRating *float64
	MinPremiereDate    *time.Time
	ImageTypes         []string
	// NoMatch short-circuits the query to an empty result; used for
	// filters nothing can satisfy (e.g. a user "likes" flag that is not
	// stored).
	NoMatch bool

	// Resumable keeps items with a saved playback position greater than
	// zero for UserID (played or not), unlike Resume.
	Resumable bool

	// BlockedTags hides items carrying any of these tags; AllowedTags, when
	// non-empty, keeps only items carrying at least one. Matching is
	// case-insensitive.
	BlockedTags []string
	AllowedTags []string

	// Parental limits. An item's rating is its own OfficialRating text or,
	// when that has no value, its series' (rating.Parse; items.rating.*).
	// ParentalMax is the user's policy maximum and RatingMax/RatingMin the
	// MaxOfficialRating/MinOfficialRating query bounds: a rated item is
	// hidden when it exceeds a maximum (value, then sub rating) or falls
	// below the minimum; unrated items always pass them.
	// BlockUnratedTypes hides items of those types that have no rating.
	ParentalMax       *rating.Score
	RatingMax         *rating.Score
	RatingMin         *rating.Score
	BlockUnratedTypes []ItemType

	// SeasonMembers widens a ParentID that names a season to the episodes of
	// the same series that carry the season's number, wherever they sit.
	SeasonMembers *SeasonMembers

	// AdjacentTo narrows the (otherwise ordered) result to the named item
	// and its immediate previous/next neighbours.
	AdjacentTo *ItemID
}

// SeasonMembers identifies the episodes that belong to a season by number.
type SeasonMembers struct {
	SeriesID ItemID
	Index    int
}

// FolderItemTypes are the item types that group other items (or are
// container-like) and therefore count as folders for IsFolder filtering.
var FolderItemTypes = []ItemType{
	ItemTypeSeries, ItemTypeSeason, ItemTypeMusicArtist, ItemTypeMusicAlbum,
	ItemTypeBoxSet, ItemTypePlaylist, ItemTypeFolder, ItemTypeCollectionFolder,
	ItemTypeUserView, ItemTypePhotoAlbum,
}

// ItemPage is the result of a bounded QueryItems call. TotalCount is the
// pre-pagination match count (what list responses in 04 report as
// TotalRecordCount), not the size of Items.
type ItemPage struct {
	Items      []Item
	TotalCount int
	Offset     int
}
