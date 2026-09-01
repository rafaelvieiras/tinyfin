package sqlite

import (
	"context"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) QuerySubtitles(ctx context.Context, itemID storage.ItemID) ([]storage.SubtitleRef, error) {
	const query = `SELECT id, item_id, path, language, format, is_forced, is_hearing_impaired, source FROM subtitle_refs WHERE item_id = ? ORDER BY id ASC`

	rows, err := s.ex.QueryContext(ctx, query, string(itemID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying subtitles for item %q: %w", itemID, err)
	}
	defer rows.Close()

	var refs []storage.SubtitleRef
	for rows.Next() {
		var ref storage.SubtitleRef
		var id, item string
		if err := rows.Scan(&id, &item, &ref.Path, &ref.Language, &ref.Format, &ref.IsForced, &ref.IsHearingImpaired, &ref.Source); err != nil {
			return nil, fmt.Errorf("sqlite: scanning subtitle ref row: %w", err)
		}
		ref.ID = storage.SubtitleID(id)
		ref.ItemID = storage.ItemID(item)
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating subtitle ref rows: %w", err)
	}
	return refs, nil
}

func (s *Store) UpsertSubtitle(ctx context.Context, ref storage.SubtitleRef) error {
	if ref.ID == "" {
		ref.ID = storage.SubtitleID(newID())
	}

	const query = `INSERT INTO subtitle_refs (id, item_id, path, language, format, is_forced, is_hearing_impaired, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			item_id=excluded.item_id, path=excluded.path, language=excluded.language, format=excluded.format,
			is_forced=excluded.is_forced, is_hearing_impaired=excluded.is_hearing_impaired, source=excluded.source`

	_, err := s.exec(ctx, query, string(ref.ID), string(ref.ItemID), ref.Path, ref.Language, ref.Format, ref.IsForced, ref.IsHearingImpaired, ref.Source)
	if err != nil {
		return fmt.Errorf("sqlite: upserting subtitle ref %q: %w", ref.ID, err)
	}
	return nil
}

func (s *Store) DeleteSubtitle(ctx context.Context, id storage.SubtitleID) error {
	if _, err := s.exec(ctx, `DELETE FROM subtitle_refs WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting subtitle ref %q: %w", id, err)
	}
	return nil
}
