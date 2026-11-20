package storage

import "time"

// LeafCount is how many leaves (non-folder items) sit below a folder and how
// many of them are unplayed for one user, seen through the folder marks on
// their ancestors — what FolderLeafCounts answers one folder at a time and
// LeafCountsFor answers for a page.
type LeafCount struct {
	Total    int
	Unplayed int
}

// Playstate is the storage-side counterpart of the per-user fields on the
// reference server's UserItemDataDto — durable resume position and
// watched status (docs/05's PlaystateStore comment: "explicitly storage,
// not session/realtime state ... it must survive a restart"). Fields that
// are query-time aggregates over a folder rather than per-item state
// (PlayedPercentage, UnplayedItemCount) are not stored here; Rating (a
// user rating, distinct from IsFavorite) and Likes
// is stored here as an optional value (see the Rating field).
type Playstate struct {
	Played                bool
	PlaybackPositionTicks int64
	PlayCount             int
	IsFavorite            bool
	LastPlayedDate        *time.Time
	// Rating is the user's own rating of the item and Likes a like/dislike
	// flag; nil means never set.
	Rating *float64
	Likes  *bool
	// FolderMarkedAt is the UnixNano time of the last played/unplayed mark
	// applied to this item as a folder (a series or season); zero when it
	// was never marked. A folder mark is a state of the folder itself: it
	// never writes to the leaves below it, and a leaf whose own state was
	// last changed before the mark is reported through the folder as
	// carrying the mark's played flag (FolderLeafCounts).
	FolderMarkedAt int64
	// Touch is a write-only hint: when set, SetPlaystate treats the write as
	// a deliberate change of the played state even if every played-related
	// value is already at its zero value (an explicit unmark of a leaf that
	// never had a row), so an earlier folder mark no longer applies to it.
	Touch bool
}
