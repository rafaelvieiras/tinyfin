package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// QueryKeyFrames returns the key frame times recorded for an item, if any.
// The row is a cache of what the container says: the caller compares Size and
// Mtime with the file on disk before trusting it (see storage.KeyFrameStore).
func (s *Store) QueryKeyFrames(ctx context.Context, itemID storage.ItemID) (storage.KeyFrameData, bool, error) {
	const query = `SELECT size, mtime, ticks FROM keyframe_data WHERE item_id = ?`

	var data storage.KeyFrameData
	var blob []byte
	err := s.ex.QueryRowContext(ctx, query, string(itemID)).Scan(&data.Size, &data.Mtime, &blob)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.KeyFrameData{}, false, nil
		}
		return storage.KeyFrameData{}, false, fmt.Errorf("sqlite: querying key frames for item %q: %w", itemID, err)
	}
	ticks, err := decodeTicks(blob)
	if err != nil {
		return storage.KeyFrameData{}, false, fmt.Errorf("sqlite: key frames for item %q: %w", itemID, err)
	}
	data.Ticks = ticks
	return data, true, nil
}

// SaveKeyFrames records an item's key frame times, replacing the row.
func (s *Store) SaveKeyFrames(ctx context.Context, itemID storage.ItemID, data storage.KeyFrameData) error {
	const query = `INSERT INTO keyframe_data (item_id, size, mtime, ticks) VALUES (?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, ticks = excluded.ticks`

	if _, err := s.ex.ExecContext(ctx, query, string(itemID), data.Size, data.Mtime, encodeTicks(data.Ticks)); err != nil {
		return fmt.Errorf("sqlite: saving key frames for item %q: %w", itemID, err)
	}
	return nil
}

// encodeTicks writes the times as varint deltas against the previous entry, so
// a two hour film's key frames take about 1.5 kB instead of 9 kB of fixed
// 64-bit values.
func encodeTicks(ticks []int64) []byte {
	buf := make([]byte, 0, len(ticks)*2)
	var prev int64
	for _, t := range ticks {
		d := t - prev
		if d < 0 {
			d = 0
		}
		buf = binary.AppendUvarint(buf, uint64(d))
		prev = t
	}
	return buf
}

// decodeTicks is the inverse of encodeTicks.
func decodeTicks(buf []byte) ([]int64, error) {
	var out []int64
	var cur int64
	for len(buf) > 0 {
		d, n := binary.Uvarint(buf)
		if n <= 0 {
			return nil, fmt.Errorf("malformed key frame ticks")
		}
		buf = buf[n:]
		cur += int64(d)
		out = append(out, cur)
	}
	return out, nil
}
