package storage

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
	LibraryID  *LibraryID
	ParentID   *ItemID
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
	// otherwise, same as Played/Favorite above.
	Resume bool
	SortBy []SortField
	Limit  int
	Offset int
}

// ItemPage is the result of a bounded QueryItems call. TotalCount is the
// pre-pagination match count (what list responses in 04 report as
// TotalRecordCount), not the size of Items.
type ItemPage struct {
	Items      []Item
	TotalCount int
	Offset     int
}
