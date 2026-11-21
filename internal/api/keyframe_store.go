package api

import (
	"context"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// keyFrameStore adapts the catalog to the transcode package's key frame
// store. The transcode package works with plain strings and never imports
// storage (see its package comment), so the two are joined here: the item id
// goes in as a string and the record comes back as ticks.
type keyFrameStore struct {
	store storage.Store
}

// LoadKeyFrames returns the key frame times recorded for an item.
func (k keyFrameStore) LoadKeyFrames(ctx context.Context, itemID string) (transcode.KeyFrameRecord, bool, error) {
	data, ok, err := k.store.QueryKeyFrames(ctx, storage.ItemID(itemID))
	if err != nil || !ok {
		return transcode.KeyFrameRecord{}, false, err
	}
	return transcode.KeyFrameRecord{Size: data.Size, Mtime: data.Mtime, Ticks: data.Ticks}, true, nil
}

// SaveKeyFrames records an item's key frame times.
func (k keyFrameStore) SaveKeyFrames(ctx context.Context, itemID string, rec transcode.KeyFrameRecord) error {
	return k.store.SaveKeyFrames(ctx, storage.ItemID(itemID), storage.KeyFrameData{
		Size:  rec.Size,
		Mtime: rec.Mtime,
		Ticks: rec.Ticks,
	})
}
