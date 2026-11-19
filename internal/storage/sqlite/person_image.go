package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// personImageColumns is what every read of person_images selects, in the order
// scanPersonImage expects.
const personImageColumns = `person_id, source, path, tag, blur_hash, width, height, size, failed_at`

// GetPersonImage answers PersonImageStore.GetPersonImage. A person with no row
// is (zero, false, nil) — not an error — so the caller can tell "no photo
// anywhere" from "not downloaded yet".
func (s *Store) GetPersonImage(ctx context.Context, personID storage.ItemID) (storage.PersonImage, bool, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT `+personImageColumns+` FROM person_images WHERE person_id = ?`, string(personID))
	img, err := scanPersonImage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.PersonImage{}, false, nil
		}
		return storage.PersonImage{}, false, fmt.Errorf("sqlite: querying image of person %q: %w", personID, err)
	}
	return img, true, nil
}

// QueryPersonImages answers PersonImageStore.QueryPersonImages with one query
// for the whole id list: a detail response carries every credit of an item, so
// asking one by one would be a query per actor.
func (s *Store) QueryPersonImages(ctx context.Context, ids []storage.ItemID) (map[storage.ItemID]storage.PersonImage, error) {
	out := map[storage.ItemID]storage.PersonImage{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, string(id))
	}
	query := `SELECT ` + personImageColumns + ` FROM person_images WHERE person_id IN (?` +
		strings.Repeat(",?", len(args)-1) + `)`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying %d person images: %w", len(ids), err)
	}
	defer rows.Close()
	for rows.Next() {
		img, err := scanPersonImage(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scanning person image row: %w", err)
		}
		out[img.PersonID] = img
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating person image rows: %w", err)
	}
	return out, nil
}

// UpsertPersonImage answers PersonImageStore.UpsertPersonImage: a full write of
// a row, which is what a download attempt records (the cached picture on
// success, FailedAt on failure).
func (s *Store) UpsertPersonImage(ctx context.Context, img storage.PersonImage) error {
	const query = `INSERT INTO person_images (` + personImageColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(person_id) DO UPDATE SET
			source=excluded.source, path=excluded.path, tag=excluded.tag,
			blur_hash=excluded.blur_hash, width=excluded.width, height=excluded.height,
			size=excluded.size, failed_at=excluded.failed_at`
	if _, err := s.exec(ctx, query, string(img.PersonID), img.Source, img.Path, img.Tag, img.BlurHash,
		img.Width, img.Height, img.Size, unixOrZero(img.FailedAt)); err != nil {
		return fmt.Errorf("sqlite: upserting image of person %q: %w", img.PersonID, err)
	}
	return nil
}

// UpsertPersonImageSource answers PersonImageStore.UpsertPersonImageSource.
// The interesting case is the second and later scan of a credit whose URL did
// not change: the cached picture and its failure record must survive, or every
// scan would throw away photos the server had already downloaded. A URL that
// did change invalidates them instead, keeping the old picture from being
// served under a tag derived from the new URL.
func (s *Store) UpsertPersonImageSource(ctx context.Context, personID storage.ItemID, source string) error {
	return upsertPersonImageSource(ctx, s.exec, personID, source)
}

// upsertPersonImageSourceSQL is the statement both the API-facing method and
// the scan-side registration run.
const upsertPersonImageSourceSQL = `INSERT INTO person_images (person_id, source) VALUES (?, ?)
		ON CONFLICT(person_id) DO UPDATE SET
			path      = CASE WHEN person_images.source = excluded.source THEN person_images.path      ELSE '' END,
			tag       = CASE WHEN person_images.source = excluded.source THEN person_images.tag       ELSE '' END,
			blur_hash = CASE WHEN person_images.source = excluded.source THEN person_images.blur_hash ELSE '' END,
			width     = CASE WHEN person_images.source = excluded.source THEN person_images.width     ELSE 0 END,
			height    = CASE WHEN person_images.source = excluded.source THEN person_images.height    ELSE 0 END,
			size      = CASE WHEN person_images.source = excluded.source THEN person_images.size      ELSE 0 END,
			failed_at = CASE WHEN person_images.source = excluded.source THEN person_images.failed_at ELSE 0 END,
			source    = excluded.source`

// execFunc is the one statement runner the source upsert needs. It is passed in
// rather than the store or a transaction, so the same statement serves both:
// *Store's write-mutex-guarded exec from the API, and the transaction a scan
// already holds from registerNamed.
type execFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)

// upsertPersonImageSource runs the source upsert through exec.
func upsertPersonImageSource(ctx context.Context, exec execFunc, personID storage.ItemID, source string) error {
	if _, err := exec(ctx, upsertPersonImageSourceSQL, string(personID), source); err != nil {
		return fmt.Errorf("sqlite: upserting image source of person %q: %w", personID, err)
	}
	return nil
}

func scanPersonImage(sc rowScanner) (storage.PersonImage, error) {
	var img storage.PersonImage
	var personID string
	var failedAt int64
	if err := sc.Scan(&personID, &img.Source, &img.Path, &img.Tag, &img.BlurHash,
		&img.Width, &img.Height, &img.Size, &failedAt); err != nil {
		return storage.PersonImage{}, err
	}
	img.PersonID = storage.ItemID(personID)
	img.FailedAt = timeOrZero(failedAt)
	return img, nil
}

// unixOrZero is time.Time -> the INTEGER a person_images timestamp column
// holds; the zero time is 0, which is what "never" reads as.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// timeOrZero is the inverse of unixOrZero.
func timeOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}
