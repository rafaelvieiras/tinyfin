package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func scanLibrary(rs rowScanner) (storage.Library, error) {
	var (
		id, name, collectionType string
		pathsJSON                string
		lastScannedAt            sql.NullTime
		libraryOptions           string
	)
	if err := rs.Scan(&id, &name, &collectionType, &pathsJSON, &lastScannedAt, &libraryOptions); err != nil {
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
	if libraryOptions != "" {
		lib.LibraryOptions = []byte(libraryOptions)
	}
	if err := unmarshalJSONInto(pathsJSON, &lib.Paths); err != nil {
		return storage.Library{}, err
	}
	return lib, nil
}

func (s *Store) GetLibrary(ctx context.Context, id storage.LibraryID) (storage.Library, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT id, name, collection_type, paths, last_scanned_at, library_options FROM libraries WHERE id = ?`, string(id))
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
	return s.queryLibraries(ctx, `ORDER BY name ASC`)
}

// QueryLibrariesNewestFirst lists the libraries in the reverse of the order
// they were created in, which is the order GET /Library/VirtualFolders reports
// (contract: admin.libraries.R5). SQLite's rowid is that order: the table has
// a TEXT primary key, so the implicit rowid still grows with every insert, and
// a rename updates the row in place instead of moving it.
func (s *Store) QueryLibrariesNewestFirst(ctx context.Context) ([]storage.Library, error) {
	return s.queryLibraries(ctx, `ORDER BY rowid DESC`)
}

func (s *Store) queryLibraries(ctx context.Context, order string) ([]storage.Library, error) {
	rows, err := s.ex.QueryContext(ctx, `SELECT id, name, collection_type, paths, last_scanned_at, library_options FROM libraries `+order)
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

	const query = `INSERT INTO libraries (id, name, collection_type, paths, last_scanned_at, library_options) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, collection_type=excluded.collection_type, paths=excluded.paths, last_scanned_at=excluded.last_scanned_at, library_options=excluded.library_options`

	_, err = s.exec(ctx, query, string(lib.ID), lib.Name, string(lib.CollectionType), pathsJSON, ptrToAny(lib.LastScannedAt), string(lib.LibraryOptions))
	if err != nil {
		return fmt.Errorf("sqlite: upserting library %q: %w", lib.ID, err)
	}
	return nil
}

func (s *Store) SetLibraryScanned(ctx context.Context, id storage.LibraryID, at time.Time) error {
	if _, err := s.exec(ctx, `UPDATE libraries SET last_scanned_at = ? WHERE id = ?`, ptrToAny(&at), string(id)); err != nil {
		return fmt.Errorf("sqlite: marking library %q scanned: %w", id, err)
	}
	return nil
}

func (s *Store) DeleteLibrary(ctx context.Context, id storage.LibraryID) error {
	if _, err := s.exec(ctx, `DELETE FROM libraries WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting library %q: %w", id, err)
	}
	return nil
}
