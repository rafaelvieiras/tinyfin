package api

import (
	"context"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// dateLastMediaAdded answers "when was something added into this folder last",
// the field the reference publishes as DateLastMediaAdded. Measured on the
// reference (items.latest.R6):
//
//   - a Series reports the newest addition inside it (the newest episode's
//     DateCreated), while its own DateCreated stays the day the series item was
//     created — the two are different values and the home row is ordered by
//     this one;
//   - a Season and a library's root folder report the zero time;
//   - a Movie and an Episode do not report the member at all.
//
// The value is only produced when the request asks for it through Fields; the
// reference omits the member otherwise.
func (s *Server) dateLastMediaAdded(ctx context.Context, item storage.Item) *wireTime {
	switch item.Type {
	case storage.ItemTypeSeries:
		seriesID := item.ID
		page, err := s.store.QueryItems(ctx, storage.ItemFilter{
			SeriesID: &seriesID,
			ItemTypes: []storage.ItemType{
				storage.ItemTypeEpisode, storage.ItemTypeSeason,
			},
			SortBy: []storage.SortField{{By: storage.SortByDateCreated, Descending: true}},
			Limit:  1,
		})
		if err != nil || len(page.Items) == 0 {
			return nil
		}
		latest := wireTime{page.Items[0].DateCreated}
		return &latest
	case storage.ItemTypeSeason, storage.ItemTypeCollectionFolder:
		zero := wireTime{}
		return &zero
	}
	return nil
}

// applyDateLastMediaAdded sets the member when the caller asked for it, which
// is the only way the reference publishes it.
func (s *Server) applyDateLastMediaAdded(ctx context.Context, dto *baseItemDto, item storage.Item, fields map[string]bool) {
	if !fields["datelastmediaadded"] {
		return
	}
	if v := s.dateLastMediaAdded(ctx, item); v != nil {
		dto.DateLastMediaAdded = v
	}
}
