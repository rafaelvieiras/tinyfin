package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) GetPlaylistAccess(ctx context.Context, id storage.ItemID) (storage.PlaylistAccess, error) {
	a := storage.PlaylistAccess{PlaylistID: id}
	var owner string
	var open int
	err := s.ex.QueryRowContext(ctx, `SELECT owner_id, open_access, media_type FROM playlist_access WHERE playlist_id = ?`, string(id)).
		Scan(&owner, &open, &a.MediaType)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("sqlite: playlist %q has no access record: %w", id, storage.ErrNotFound)
	}
	if err != nil {
		return a, fmt.Errorf("sqlite: reading playlist access: %w", err)
	}
	a.OwnerID, a.OpenAccess = storage.UserID(owner), open != 0
	rows, err := s.ex.QueryContext(ctx, `SELECT user_id, can_edit FROM playlist_shares WHERE playlist_id = ? ORDER BY position, user_id`, string(id))
	if err != nil {
		return a, fmt.Errorf("sqlite: reading playlist shares: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		var edit int
		if err := rows.Scan(&u, &edit); err != nil {
			return a, err
		}
		a.Shares = append(a.Shares, storage.PlaylistShare{UserID: storage.UserID(u), CanEdit: edit != 0})
	}
	return a, rows.Err()
}

func (s *Store) SetPlaylistAccess(ctx context.Context, a storage.PlaylistAccess) error {
	return s.withTx(ctx, func(ex dbtx) error {
		if _, err := ex.ExecContext(ctx, `INSERT INTO playlist_access (playlist_id, owner_id, open_access, media_type) VALUES (?,?,?,?)
			ON CONFLICT(playlist_id) DO UPDATE SET owner_id=excluded.owner_id, open_access=excluded.open_access, media_type=excluded.media_type`,
			string(a.PlaylistID), string(a.OwnerID), boolToInt(a.OpenAccess), a.MediaType); err != nil {
			return fmt.Errorf("sqlite: writing playlist access: %w", err)
		}
		if _, err := ex.ExecContext(ctx, `DELETE FROM playlist_shares WHERE playlist_id = ?`, string(a.PlaylistID)); err != nil {
			return err
		}
		for i, sh := range a.Shares {
			if _, err := ex.ExecContext(ctx, `INSERT OR REPLACE INTO playlist_shares (playlist_id, user_id, can_edit, position) VALUES (?,?,?,?)`,
				string(a.PlaylistID), string(sh.UserID), boolToInt(sh.CanEdit), i); err != nil {
				return fmt.Errorf("sqlite: writing playlist share: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) PlaylistEntries(ctx context.Context, id storage.ItemID) ([]storage.ItemID, error) {
	rows, err := s.ex.QueryContext(ctx,
		`SELECT child_id FROM item_relations WHERE parent_id = ? AND kind = ? ORDER BY sort_index ASC, rowid ASC`,
		string(id), storage.RelationPlaylist)
	if err != nil {
		return nil, fmt.Errorf("sqlite: reading playlist entries: %w", err)
	}
	defer rows.Close()
	var out []storage.ItemID
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, storage.ItemID(c))
	}
	return out, rows.Err()
}

func (s *Store) SetPlaylistEntries(ctx context.Context, id storage.ItemID, entries []storage.ItemID) error {
	return s.withTx(ctx, func(ex dbtx) error {
		if _, err := ex.ExecContext(ctx, `DELETE FROM item_relations WHERE parent_id = ? AND kind = ?`, string(id), storage.RelationPlaylist); err != nil {
			return fmt.Errorf("sqlite: clearing playlist entries: %w", err)
		}
		for i, e := range entries {
			if _, err := ex.ExecContext(ctx, `INSERT INTO item_relations (id, parent_id, child_id, kind, sort_index) VALUES (?,?,?,?,?)`,
				newID(), string(id), string(e), storage.RelationPlaylist, i); err != nil {
				return fmt.Errorf("sqlite: writing playlist entry: %w", err)
			}
		}
		return nil
	})
}
