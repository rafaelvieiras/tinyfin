package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) GetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID) (storage.Playstate, error) {
	const query = `SELECT played, playback_position_ticks, play_count, is_favorite, last_played_date FROM playstates WHERE user_id = ? AND item_id = ?`

	var (
		ps             storage.Playstate
		lastPlayedDate sql.NullTime
	)
	row := s.ex.QueryRowContext(ctx, query, string(userID), string(itemID))
	err := row.Scan(&ps.Played, &ps.PlaybackPositionTicks, &ps.PlayCount, &ps.IsFavorite, &lastPlayedDate)
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
	return ps, nil
}

func (s *Store) SetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID, state storage.Playstate) error {
	const query = `INSERT INTO playstates (user_id, item_id, played, playback_position_ticks, play_count, is_favorite, last_played_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			played=excluded.played, playback_position_ticks=excluded.playback_position_ticks, play_count=excluded.play_count,
			is_favorite=excluded.is_favorite, last_played_date=excluded.last_played_date`

	_, err := s.ex.ExecContext(ctx, query, string(userID), string(itemID), state.Played, state.PlaybackPositionTicks, state.PlayCount, state.IsFavorite, ptrToAny(state.LastPlayedDate))
	if err != nil {
		return fmt.Errorf("sqlite: setting playstate for user %q item %q: %w", userID, itemID, err)
	}
	return nil
}
