package api

import (
	"context"
	"sort"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// latestCard is a grouped "recently added" card together with the date that
// positions it in the row. A series card's item carries the series' own
// DateCreated, while the row is ordered by the newest addition inside the
// series (storage.SeriesActivity.Latest) — the reference does the same: it
// returns the series with its own DateCreated and dates it by the episode.
type latestCard struct {
	item   storage.Item
	latest time.Time
}

// latestGroupedMixed implements docs/12's "Items/Latest — series grouping" for
// a request that names no library, which is the row the home screen asks for
// (items.latest.R4). The scope's flat media (movies, albums, anything else the
// filter allows) comes from one query; every visible TV library contributes its
// series cards; the two are merged by date and cut to the requested limit.
//
// limit counts cards, not raw episodes, and the merge happens before the cut,
// so a series that filled the newest five rows still answers one card and does
// not push the movies out of the row. The reference applies the limit to the
// raw rows first and can answer fewer cards than asked for (DIV-36).
func (s *Server) latestGroupedMixed(ctx context.Context, filter *storage.ItemFilter, scope itemsScope, u storage.User, wantLimit int, keepLoose bool) ([]storage.Item, error) {
	type dated struct {
		item storage.Item
		at   time.Time
	}

	rows := make([]dated, 0, wantLimit)

	// Flat media: the show rows are dropped here and come back as cards below.
	flat := *filter
	flat.ExcludeItemTypes = append(append([]storage.ItemType{}, filter.ExcludeItemTypes...),
		storage.ItemTypeEpisode, storage.ItemTypeSeason, storage.ItemTypeSeries)
	flat.Limit = wantLimit
	page, err := s.store.QueryItems(ctx, flat)
	if err != nil {
		return nil, err
	}
	flatItems := page.Items
	if keepLoose {
		flatItems = s.dropAlbumTracks(ctx, flatItems)
	}
	for _, it := range flatItems {
		if s.itemReadable(ctx, scope, it) {
			rows = append(rows, dated{item: it, at: it.DateCreated})
		}
	}

	// One card per series, per visible TV library.
	for _, lib := range scope.libs {
		if !scope.libIDs[lib.ID] || lib.CollectionType != storage.CollectionTypeTVShows {
			continue
		}
		cards, err := s.tvShowsLatestGrouped(ctx, lib.ID, wantLimit, u.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cards {
			if s.itemReadable(ctx, scope, c.item) {
				rows = append(rows, dated{item: c.item, at: c.latest})
			}
		}
	}

	// Newest first. Equal dates keep the order the rows arrived in: the
	// reference does not order ties by name either, so nothing better is
	// available to assert (items.latest.R4).
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })

	if len(rows) > wantLimit {
		rows = rows[:wantLimit]
	}
	out := make([]storage.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.item)
	}
	return out, nil
}

// dropAlbumTracks drops the tracks that sit inside an album item, keeping the
// loose ones (music.latest.R4): a folder-like row lists albums, and a track
// only stands for itself when it has no album item of its own.
func (s *Server) dropAlbumTracks(ctx context.Context, items []storage.Item) []storage.Item {
	kept := make([]storage.Item, 0, len(items))
	for _, it := range items {
		if it.Type == storage.ItemTypeAudio && it.ParentID != nil {
			if p, err := s.store.GetItem(ctx, *it.ParentID); err == nil && p.Type == storage.ItemTypeMusicAlbum {
				continue
			}
		}
		kept = append(kept, it)
	}
	return kept
}
