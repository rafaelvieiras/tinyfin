package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) GetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID) (storage.Playstate, error) {
	const query = `SELECT played, playback_position_ticks, play_count, is_favorite, last_played_date, rating, likes FROM playstates WHERE user_id = ? AND item_id = ?`

	var (
		ps             storage.Playstate
		lastPlayedDate sql.NullTime
		rating         sql.NullFloat64
		likes          sql.NullBool
	)
	row := s.ex.QueryRowContext(ctx, query, string(userID), string(itemID))
	err := row.Scan(&ps.Played, &ps.PlaybackPositionTicks, &ps.PlayCount, &ps.IsFavorite, &lastPlayedDate, &rating, &likes)
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
	return ps, nil
}

func (s *Store) SetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID, state storage.Playstate) error {
	const query = `INSERT INTO playstates (user_id, item_id, played, playback_position_ticks, play_count, is_favorite, last_played_date, rating, likes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			played=excluded.played, playback_position_ticks=excluded.playback_position_ticks, play_count=excluded.play_count,
			is_favorite=excluded.is_favorite, last_played_date=excluded.last_played_date,
			rating=excluded.rating, likes=excluded.likes`

	_, err := s.exec(ctx, query, string(userID), string(itemID), state.Played, state.PlaybackPositionTicks, state.PlayCount, state.IsFavorite, ptrToAny(state.LastPlayedDate), ptrToAny(state.Rating), ptrToAny(state.Likes))
	if err != nil {
		return fmt.Errorf("sqlite: setting playstate for user %q item %q: %w", userID, itemID, err)
	}
	return nil
}
