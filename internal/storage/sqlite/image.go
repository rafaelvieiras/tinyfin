package sqlite

import (
	"context"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) GetImageRefs(ctx context.Context, itemID storage.ItemID, kind storage.ImageKind) ([]storage.ImageRef, error) {
	const query = `SELECT id, item_id, kind, idx, path, tag, blur_hash, width, height, size FROM image_refs WHERE item_id = ? AND kind = ? ORDER BY idx ASC`

	rows, err := s.ex.QueryContext(ctx, query, string(itemID), string(kind))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying image refs for item %q kind %q: %w", itemID, kind, err)
	}
	defer rows.Close()

	var refs []storage.ImageRef
	for rows.Next() {
		var ref storage.ImageRef
		var id, item, k string
		if err := rows.Scan(&id, &item, &k, &ref.Index, &ref.Path, &ref.Tag, &ref.BlurHash, &ref.Width, &ref.Height, &ref.Size); err != nil {
			return nil, fmt.Errorf("sqlite: scanning image ref row: %w", err)
		}
		ref.ID = storage.ImageRefID(id)
		ref.ItemID = storage.ItemID(item)
		ref.Kind = storage.ImageKind(k)
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating image ref rows: %w", err)
	}
	return refs, nil
}

func (s *Store) UpsertImageRef(ctx context.Context, ref storage.ImageRef) error {
	if ref.ID == "" {
		ref.ID = storage.ImageRefID(newID())
	}

	const query = `INSERT INTO image_refs (id, item_id, kind, idx, path, tag, blur_hash, width, height, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			item_id=excluded.item_id, kind=excluded.kind, idx=excluded.idx, path=excluded.path, tag=excluded.tag,
			blur_hash=excluded.blur_hash, width=excluded.width, height=excluded.height, size=excluded.size`

	_, err := s.exec(ctx, query, string(ref.ID), string(ref.ItemID), string(ref.Kind), ref.Index, ref.Path, ref.Tag, ref.BlurHash, ref.Width, ref.Height, ref.Size)
	if err != nil {
		return fmt.Errorf("sqlite: upserting image ref %q: %w", ref.ID, err)
	}
	return nil
}

func (s *Store) DeleteImageRef(ctx context.Context, id storage.ImageRefID) error {
	if _, err := s.exec(ctx, `DELETE FROM image_refs WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting image ref %q: %w", id, err)
	}
	return nil
}

func (s *Store) QueryTrickplay(ctx context.Context, itemID storage.ItemID) ([]storage.TrickplayInfo, error) {
	const query = `SELECT width, height, tile_width, tile_height, thumbnail_count, interval_ms, bandwidth FROM trickplay_info WHERE item_id = ? ORDER BY width ASC`

	rows, err := s.ex.QueryContext(ctx, query, string(itemID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying trickplay info for item %q: %w", itemID, err)
	}
	defer rows.Close()

	var infos []storage.TrickplayInfo
	for rows.Next() {
		var info storage.TrickplayInfo
		if err := rows.Scan(&info.Width, &info.Height, &info.TileWidth, &info.TileHeight, &info.ThumbnailCount, &info.Interval, &info.Bandwidth); err != nil {
			return nil, fmt.Errorf("sqlite: scanning trickplay info row: %w", err)
		}
		infos = append(infos, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating trickplay info rows: %w", err)
	}
	return infos, nil
}

func (s *Store) UpsertTrickplayInfo(ctx context.Context, itemID storage.ItemID, info storage.TrickplayInfo) error {
	const query = `INSERT INTO trickplay_info (item_id, width, height, tile_width, tile_height, thumbnail_count, interval_ms, bandwidth) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id, width) DO UPDATE SET
			height=excluded.height, tile_width=excluded.tile_width, tile_height=excluded.tile_height,
			thumbnail_count=excluded.thumbnail_count, interval_ms=excluded.interval_ms, bandwidth=excluded.bandwidth`

	_, err := s.exec(ctx, query, string(itemID), info.Width, info.Height, info.TileWidth, info.TileHeight, info.ThumbnailCount, info.Interval, info.Bandwidth)
	if err != nil {
		return fmt.Errorf("sqlite: upserting trickplay info for item %q width %d: %w", itemID, info.Width, err)
	}
	return nil
}

func (s *Store) DeleteTrickplayInfo(ctx context.Context, itemID storage.ItemID, width int) error {
	if _, err := s.exec(ctx, `DELETE FROM trickplay_info WHERE item_id = ? AND width = ?`, string(itemID), width); err != nil {
		return fmt.Errorf("sqlite: deleting trickplay info for item %q width %d: %w", itemID, width, err)
	}
	return nil
}
