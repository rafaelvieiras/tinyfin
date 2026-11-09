package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// GetLibraryImage answers LibraryImageStore.GetLibraryImage. A library with no
// image of that kind is (zero, false, nil) — not an error — so the caller can
// tell "absent" from "stored".
func (s *Store) GetLibraryImage(ctx context.Context, libraryID storage.LibraryID, kind storage.ImageKind) (storage.LibraryImage, bool, error) {
	const query = `SELECT path, tag, blur_hash, width, height, size FROM library_images WHERE library_id = ? AND kind = ?`

	img := storage.LibraryImage{LibraryID: libraryID, Kind: kind}
	err := s.ex.QueryRowContext(ctx, query, string(libraryID), string(kind)).
		Scan(&img.Path, &img.Tag, &img.BlurHash, &img.Width, &img.Height, &img.Size)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.LibraryImage{}, false, nil
		}
		return storage.LibraryImage{}, false, fmt.Errorf("sqlite: querying image of library %q kind %q: %w", libraryID, kind, err)
	}
	return img, true, nil
}

func (s *Store) QueryLibraryImages(ctx context.Context) (map[storage.LibraryID]storage.LibraryImage, error) {
	const query = `SELECT library_id, kind, path, tag, blur_hash, width, height, size FROM library_images`

	rows, err := s.ex.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying library images: %w", err)
	}
	defer rows.Close()

	out := map[storage.LibraryID]storage.LibraryImage{}
	for rows.Next() {
		var img storage.LibraryImage
		var libraryID, kind string
		if err := rows.Scan(&libraryID, &kind, &img.Path, &img.Tag, &img.BlurHash, &img.Width, &img.Height, &img.Size); err != nil {
			return nil, fmt.Errorf("sqlite: scanning library image row: %w", err)
		}
		img.LibraryID = storage.LibraryID(libraryID)
		img.Kind = storage.ImageKind(kind)
		out[img.LibraryID] = img
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating library image rows: %w", err)
	}
	return out, nil
}

func (s *Store) UpsertLibraryImage(ctx context.Context, img storage.LibraryImage) error {
	const query = `INSERT INTO library_images (library_id, kind, path, tag, blur_hash, width, height, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(library_id, kind) DO UPDATE SET
			path=excluded.path, tag=excluded.tag, blur_hash=excluded.blur_hash,
			width=excluded.width, height=excluded.height, size=excluded.size`

	_, err := s.exec(ctx, query, string(img.LibraryID), string(img.Kind), img.Path, img.Tag, img.BlurHash, img.Width, img.Height, img.Size)
	if err != nil {
		return fmt.Errorf("sqlite: upserting image of library %q kind %q: %w", img.LibraryID, img.Kind, err)
	}
	return nil
}

func (s *Store) DeleteLibraryImage(ctx context.Context, libraryID storage.LibraryID, kind storage.ImageKind) error {
	if _, err := s.exec(ctx, `DELETE FROM library_images WHERE library_id = ? AND kind = ?`, string(libraryID), string(kind)); err != nil {
		return fmt.Errorf("sqlite: deleting image of library %q kind %q: %w", libraryID, kind, err)
	}
	return nil
}
