package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func scanLibrary(rs rowScanner) (storage.Library, error) {
	var (
		id, name, collectionType string
		pathsJSON                string
		lastScannedAt            sql.NullTime
	)
	if err := rs.Scan(&id, &name, &collectionType, &pathsJSON, &lastScannedAt); err != nil {
		return storage.Library{}, err
	}

	lib := storage.Library{
		ID:             storage.LibraryID(id),
		Name:           name,
		CollectionType: storage.CollectionType(collectionType),
	}
	if lastScannedAt.Valid {
		v := lastScannedAt.Time
		lib.LastScannedAt = &v
	}
	if err := unmarshalJSONInto(pathsJSON, &lib.Paths); err != nil {
		return storage.Library{}, err
	}
	return lib, nil
}

func (s *Store) GetLibrary(ctx context.Context, id storage.LibraryID) (storage.Library, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT id, name, collection_type, paths, last_scanned_at FROM libraries WHERE id = ?`, string(id))
	lib, err := scanLibrary(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.Library{}, fmt.Errorf("sqlite: library %q not found: %w", id, err)
		}
		return storage.Library{}, fmt.Errorf("sqlite: getting library %q: %w", id, err)
	}
	return lib, nil
}

func (s *Store) QueryLibraries(ctx context.Context) ([]storage.Library, error) {
	rows, err := s.ex.QueryContext(ctx, `SELECT id, name, collection_type, paths, last_scanned_at FROM libraries ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying libraries: %w", err)
	}
	defer rows.Close()

	var libs []storage.Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scanning library row: %w", err)
		}
		libs = append(libs, lib)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating library rows: %w", err)
	}
	return libs, nil
}

func (s *Store) UpsertLibrary(ctx context.Context, lib storage.Library) error {
	if lib.ID == "" {
		lib.ID = storage.LibraryID(newID())
	}

	pathsJSON, err := marshalJSON(lib.Paths)
	if err != nil {
		return err
	}

	const query = `INSERT INTO libraries (id, name, collection_type, paths, last_scanned_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, collection_type=excluded.collection_type, paths=excluded.paths, last_scanned_at=excluded.last_scanned_at`

	_, err = s.exec(ctx, query, string(lib.ID), lib.Name, string(lib.CollectionType), pathsJSON, ptrToAny(lib.LastScannedAt))
	if err != nil {
		return fmt.Errorf("sqlite: upserting library %q: %w", lib.ID, err)
	}
	return nil
}

func (s *Store) DeleteLibrary(ctx context.Context, id storage.LibraryID) error {
	if _, err := s.exec(ctx, `DELETE FROM libraries WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting library %q: %w", id, err)
	}
	return nil
}
