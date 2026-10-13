package storage

import "time"

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
}
