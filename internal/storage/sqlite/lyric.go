package sqlite

import (
	"context"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) QueryLyrics(ctx context.Context, itemID storage.ItemID) ([]storage.LyricRef, error) {
	const query = `SELECT id, item_id, path, format, author, is_synced, source FROM lyric_refs WHERE item_id = ? ORDER BY id ASC`

	rows, err := s.ex.QueryContext(ctx, query, string(itemID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying lyrics for item %q: %w", itemID, err)
	}
	defer rows.Close()

	var refs []storage.LyricRef
	for rows.Next() {
		var ref storage.LyricRef
		var id, item string
		if err := rows.Scan(&id, &item, &ref.Path, &ref.Format, &ref.Author, &ref.IsSynced, &ref.Source); err != nil {
			return nil, fmt.Errorf("sqlite: scanning lyric ref row: %w", err)
		}
		ref.ID = storage.LyricID(id)
		ref.ItemID = storage.ItemID(item)
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating lyric ref rows: %w", err)
	}
	return refs, nil
}

func (s *Store) UpsertLyric(ctx context.Context, ref storage.LyricRef) error {
	if ref.ID == "" {
		ref.ID = storage.LyricID(newID())
	}

	const query = `INSERT INTO lyric_refs (id, item_id, path, format, author, is_synced, source) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			item_id=excluded.item_id, path=excluded.path, format=excluded.format, author=excluded.author,
			is_synced=excluded.is_synced, source=excluded.source`

	_, err := s.exec(ctx, query, string(ref.ID), string(ref.ItemID), ref.Path, ref.Format, ref.Author, ref.IsSynced, ref.Source)
	if err != nil {
		return fmt.Errorf("sqlite: upserting lyric ref %q: %w", ref.ID, err)
	}
	return nil
}

func (s *Store) DeleteLyric(ctx context.Context, id storage.LyricID) error {
	if _, err := s.exec(ctx, `DELETE FROM lyric_refs WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting lyric ref %q: %w", id, err)
	}
	return nil
}
