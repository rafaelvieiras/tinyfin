package storage

import "time"

// Playstate is the storage-side counterpart of the per-user fields on the
// reference server's UserItemDataDto — durable resume position and
// watched status (docs/05's PlaystateStore comment: "explicitly storage,
// not session/realtime state ... it must survive a restart"). Fields that
// are query-time aggregates over a folder rather than per-item state
// (PlayedPercentage, UnplayedItemCount) and the deprecated Likes field are
// not stored here; Rating (a 0-10 user rating, distinct from IsFavorite)
// is left out for the same "minimal over guessed" reason as elsewhere —
// nothing in docs/04's Phase 1 priority groups exercises it yet.
type Playstate struct {
	Played                bool
	PlaybackPositionTicks int64
	PlayCount             int
	IsFavorite            bool
	LastPlayedDate        *time.Time
}
