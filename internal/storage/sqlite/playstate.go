package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) GetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID) (storage.Playstate, error) {
	const query = `SELECT played, playback_position_ticks, play_count, is_favorite, last_played_date, rating, likes, folder_marked_at FROM playstates WHERE user_id = ? AND item_id = ?`

	var (
		ps             storage.Playstate
		lastPlayedDate sql.NullTime
		rating         sql.NullFloat64
		likes          sql.NullBool
		markedAt       sql.NullInt64
	)
	row := s.ex.QueryRowContext(ctx, query, string(userID), string(itemID))
	err := row.Scan(&ps.Played, &ps.PlaybackPositionTicks, &ps.PlayCount, &ps.IsFavorite, &lastPlayedDate, &rating, &likes, &markedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No playstate recorded yet is a normal state (never played,
			// not favorited), not an error — return the zero value.
			return storage.Playstate{}, nil
		}
		return storage.Playstate{}, fmt.Errorf("sqlite: getting playstate for user %q item %q: %w", userID, itemID, err)
	}
	if lastPlayedDate.Valid {
		v := lastPlayedDate.Time
		ps.LastPlayedDate = &v
	}
	if rating.Valid {
		v := rating.Float64
		ps.Rating = &v
	}
	if likes.Valid {
		v := likes.Bool
		ps.Likes = &v
	}
	if markedAt.Valid {
		ps.FolderMarkedAt = markedAt.Int64
	}
	return ps, nil
}

// SetPlaystate upserts the row. touched_at records when the played flag,
// play count, position or last-played date last changed (a favorite or a
// rating alone never counts), which is what a folder mark is compared
// against in FolderLeafCounts.
func (s *Store) SetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID, state storage.Playstate) error {
	const query = `INSERT INTO playstates (user_id, item_id, played, playback_position_ticks, play_count, is_favorite, last_played_date, rating, likes, touched_at, folder_marked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			touched_at = CASE WHEN ?
				OR played IS NOT excluded.played
				OR play_count IS NOT excluded.play_count
				OR playback_position_ticks IS NOT excluded.playback_position_ticks
				OR last_played_date IS NOT excluded.last_played_date
				THEN ? ELSE touched_at END,
			played=excluded.played, playback_position_ticks=excluded.playback_position_ticks, play_count=excluded.play_count,
			is_favorite=excluded.is_favorite, last_played_date=excluded.last_played_date,
			rating=excluded.rating, likes=excluded.likes, folder_marked_at=excluded.folder_marked_at`

	now := time.Now().UnixNano()
	var insertTouched, marked any
	if state.Touch || state.Played || state.PlayCount > 0 || state.PlaybackPositionTicks > 0 || state.LastPlayedDate != nil {
		insertTouched = now
	}
	if state.FolderMarkedAt != 0 {
		marked = state.FolderMarkedAt
	}
	_, err := s.exec(ctx, query, string(userID), string(itemID), state.Played, state.PlaybackPositionTicks, state.PlayCount, state.IsFavorite,
		ptrToAny(state.LastPlayedDate), ptrToAny(state.Rating), ptrToAny(state.Likes), insertTouched, marked, state.Touch, now)
	if err != nil {
		return fmt.Errorf("sqlite: setting playstate for user %q item %q: %w", userID, itemID, err)
	}
	return nil
}

// FolderLeafCounts counts the leaves under folderID and how many are
// unplayed for userID. A leaf is played when its own row says so, unless an
// ancestor folder was marked after the leaf's own state last changed; the
// most recent such mark then decides.
func (s *Store) FolderLeafCounts(ctx context.Context, userID storage.UserID, folderID storage.ItemID) (total, unplayed int, err error) {
	query := `SELECT COUNT(*), COALESCE(SUM(CASE WHEN eff = 0 THEN 1 ELSE 0 END), 0) FROM (
		SELECT ` + effectivePlayedSQL("leaf.id") + ` AS eff
		FROM item_ancestors a JOIN items leaf ON leaf.id = a.item_id AND leaf.type NOT IN ` + folderTypeSQL() + `
		WHERE a.ancestor_id = ?)`
	u := string(userID)
	err = s.ex.QueryRowContext(ctx, query, u, u, u, string(folderID)).Scan(&total, &unplayed)
	if err != nil {
		return 0, 0, fmt.Errorf("sqlite: counting leaves of %q for user %q: %w", folderID, userID, err)
	}
	return total, unplayed, nil
}

// effectivePlayedSQL renders the played state of the leaf identified by
// leafID (a SQL expression) as seen through its ancestors' folder marks. It
// takes three positional user-id arguments.
func effectivePlayedSQL(leafID string) string {
	return `COALESCE(
		(SELECT fm.played FROM item_ancestors fa
			JOIN playstates fm ON fm.item_id = fa.ancestor_id AND fm.user_id = ?
			WHERE fa.item_id = ` + leafID + ` AND fm.folder_marked_at IS NOT NULL
			AND fm.folder_marked_at > COALESCE((SELECT lp.touched_at FROM playstates lp WHERE lp.item_id = ` + leafID + ` AND lp.user_id = ?), 0)
			ORDER BY fm.folder_marked_at DESC LIMIT 1),
		(SELECT lp.played FROM playstates lp WHERE lp.item_id = ` + leafID + ` AND lp.user_id = ?),
		0)`
}
