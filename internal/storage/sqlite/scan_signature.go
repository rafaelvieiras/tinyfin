package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// GetScanSignature returns the file size and mtime (unix ns) recorded at the
// item's last successful probe; ok is false when none is recorded.
func (s *Store) GetScanSignature(ctx context.Context, id storage.ItemID) (size, mtimeNS int64, ok bool, err error) {
	err = s.ex.QueryRowContext(ctx, `SELECT size, mtime_ns FROM scan_signatures WHERE item_id = ?`, string(id)).Scan(&size, &mtimeNS)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("sqlite: reading scan signature for %q: %w", id, err)
	}
	return size, mtimeNS, true, nil
}

// SetScanSignature records (or replaces) the item's file signature.
func (s *Store) SetScanSignature(ctx context.Context, id storage.ItemID, size, mtimeNS int64) error {
	_, err := s.exec(ctx, `INSERT INTO scan_signatures (item_id, size, mtime_ns) VALUES (?,?,?)
		ON CONFLICT(item_id) DO UPDATE SET size=excluded.size, mtime_ns=excluded.mtime_ns`, string(id), size, mtimeNS)
	if err != nil {
		return fmt.Errorf("sqlite: writing scan signature for %q: %w", id, err)
	}
	return nil
}
