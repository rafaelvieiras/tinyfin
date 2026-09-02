// latest_grouping.go implements the TV-shows branch of GET /Items/Latest's
// grouping behavior: docs/12-clean-room-behavior-specs.md's "Items/Latest —
// series grouping" section, produced under the clean-room process
// (AGENTS.md's "Clean-room requirement", D12) — this file was written from
// that spec plus black-box verification against a running reference server
// (staging :8097), never with the reference server's own source open.
//
// Confirmed live against the reference server, not assumed: the request's
// grouping toggle is a `groupItems` query param (any capitalization —
// handleItemsLatest reads it via caseInsensitiveQuery), defaulting to
// grouped when absent, exactly the default jellyfin-web's bundle relies on
// (docs/11's GET /Items/Latest note). `groupItems=false` returns the flat,
// ungrouped list handleItemsLatest already produced before this file
// existed — unchanged by anything here.
//
// Known, deliberately-not-chased limitation found during that live
// comparison: for a series whose only recent activity is older than
// roughly a month, the reference server sometimes still resolves its card
// as a single episode even when this file's window/bulk rule (straight
// from docs/12) would call it a multi-episode or multi-season burst — most
// likely some additional recency bound in the reference server beyond the
// documented 24-hour trailing window, not established precisely via
// black-box testing alone. Per docs/12's own "Scope note for the
// implementer", that is logged here rather than chased by rereading the
// reference source.
//
// Also implemented here, confirmed live via the same black-box methodology
// (docs/12's "Implementation findings"): a series every one of whose
// episodes is played (PlayCount > 0 for the requesting user, across the
// whole series — not just the items that happen to be in this file's
// candidate pool) is excluded from the grouped result entirely, and does
// not consume one of the request's limit slots by being excluded — see
// isSeriesFullyWatched and its use in tvShowsLatestGrouped below.
package api

import (
	"context"
	"sort"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// seriesLatestWindowHours is docs/12's "24-hour lookback from that series'
// latest timestamp" — the trailing window used to decide which of a
// series' recently-added items belong to the same "recent addition event"
// for card-selection purposes.
const seriesLatestWindowHours = 24

// latestSeriesGroupingPoolMin/Multiplier/Max bound the single candidate
// query tvShowsLatestGrouped issues: large enough that an active library's
// distinct recently-touched series each bring enough of their own rows into
// the pool to correctly detect a multi-episode/multi-season addition burst,
// but still one bounded query — never the whole catalog (AGENTS.md
// principle 1, "idle memory is a budget"). A series whose own items don't
// happen to fall within this pool (an old, low-volume addition sitting far
// behind a handful of very prolific series in raw recency) won't be found
// even if it would otherwise rank inside the request's own `limit` — an
// accepted tradeoff of keeping this a single bounded read rather than an
// unbounded per-series scan.
const (
	latestSeriesGroupingPoolMin        = 200
	latestSeriesGroupingPoolMultiplier = 10
	latestSeriesGroupingPoolMax        = 1000
)

func latestSeriesGroupingPoolSize(limit int) int {
	n := limit * latestSeriesGroupingPoolMultiplier
	if n < latestSeriesGroupingPoolMin {
		n = latestSeriesGroupingPoolMin
	}
	if n > latestSeriesGroupingPoolMax {
		n = latestSeriesGroupingPoolMax
	}
	return n
}

// seriesGroup accumulates the candidate Episode/Season rows belonging to
// one series, plus that series' own most recent addition timestamp (docs/12
// step 1: "for each series, note the most recent addition timestamp in that
// series").
type seriesGroup struct {
	seriesID storage.ItemID
	items    []storage.Item
	latest   time.Time
}

// groupBySeries buckets candidate Episode/Season rows by their SeriesID.
// Both types carry it directly (storage/item.go's doc comment on
// SeriesID/SeasonID: populated on both Season and Episode rows) — a row
// missing it is skipped defensively rather than panicking, since that
// should never happen for these two types but nothing here should crash a
// request over a data anomaly.
func groupBySeries(items []storage.Item) map[storage.ItemID]*seriesGroup {
	groups := make(map[storage.ItemID]*seriesGroup)
	for _, it := range items {
		if it.SeriesID == nil {
			continue
		}
		g, ok := groups[*it.SeriesID]
		if !ok {
			g = &seriesGroup{seriesID: *it.SeriesID}
			groups[*it.SeriesID] = g
		}
		g.items = append(g.items, it)
		if it.DateCreated.After(g.latest) {
			g.latest = it.DateCreated
		}
	}
	return groups
}

// rankSeriesGroups orders groups newest-addition-first (docs/12 step 2),
// with a deterministic tie-break (step 5: "a stable tie-break so ordering
// doesn't jitter between identical requests") on SeriesID when two series'
// latest timestamps are exactly equal.
func rankSeriesGroups(groups map[storage.ItemID]*seriesGroup) []*seriesGroup {
	ranked := make([]*seriesGroup, 0, len(groups))
	for _, g := range groups {
		ranked = append(ranked, g)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if !ranked[i].latest.Equal(ranked[j].latest) {
			return ranked[i].latest.After(ranked[j].latest)
		}
		return ranked[i].seriesID < ranked[j].seriesID
	})
	return ranked
}

// seasonKeyOf reports the season identity a window item belongs to for the
// "does this window touch one season or several" check (docs/12 step 3): a
// Season row is its own key, an Episode row's key is its SeasonID. Any
// other type never reaches here (the candidate query is scoped to
// Episode/Season only).
func seasonKeyOf(it storage.Item) (storage.ItemID, bool) {
	switch it.Type {
	case storage.ItemTypeSeason:
		return it.ID, true
	case storage.ItemTypeEpisode:
		if it.SeasonID != nil {
			return *it.SeasonID, true
		}
	}
	return "", false
}

// itemTypeCount is a cheap Limit=1 QueryItems call read only for its
// TotalCount — how many itemType rows exist directly under parentID.
// Backs both "how many episodes does this season have in total" and "how
// many seasons does this series have in total" (docs/12's bulk/season-vs-
// series branch), the same ParentID convention handleShowSeasons/
// handleShowEpisodes already use for those two relationships.
func (s *Server) itemTypeCount(ctx context.Context, parentID storage.ItemID, itemType storage.ItemType) (int, error) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ParentID:  &parentID,
		ItemTypes: []storage.ItemType{itemType},
		Limit:     1,
	})
	if err != nil {
		return 0, err
	}
	return page.TotalCount, nil
}

// latestItemInGroup returns g's own most recent item — used both as the
// non-bulk single-episode card and as a defensive fallback if a window
// somehow resolves to no season key at all (malformed data: an Episode row
// missing SeasonID). g.items is always non-empty (groupBySeries only
// creates a group when adding an item to it).
func latestItemInGroup(g *seriesGroup) storage.Item {
	latest := g.items[0]
	for _, it := range g.items[1:] {
		if it.DateCreated.After(latest.DateCreated) {
			latest = it
		}
	}
	return latest
}

// pickSeriesCard resolves docs/12 step 3 for one series group: which single
// item — a Season, the Series itself, or one Episode — represents it in the
// final result. Returns nil, nil if the chosen representative can no longer
// be read (e.g. deleted between the candidate query and here) rather than
// failing the whole request over one series.
//
// docs/12 step 4 (falling back to the single latest episode when the chosen
// entity fails a per-user visibility check) is intentionally not
// implemented: Tinyfin has no parental/user-visibility concept yet
// (internal/api/users.go's confirmed "every user is public" note), so that
// check is a no-op today — every entity resolved here is used directly.
func (s *Server) pickSeriesCard(ctx context.Context, g *seriesGroup) (*storage.Item, error) {
	windowStart := g.latest.Add(-seriesLatestWindowHours * time.Hour)

	var window []storage.Item
	for _, it := range g.items {
		if !it.DateCreated.Before(windowStart) {
			window = append(window, it)
		}
	}

	seasons := make(map[storage.ItemID]bool)
	for _, it := range window {
		if sid, ok := seasonKeyOf(it); ok {
			seasons[sid] = true
		}
	}

	switch len(seasons) {
	case 0:
		// No resolvable season key in the window (defensive only — see
		// seasonKeyOf) — fall through to the single-latest-item case below.

	case 1:
		var seasonID storage.ItemID
		for sid := range seasons {
			seasonID = sid
		}

		episodeCount := 0
		for _, it := range window {
			if it.Type == storage.ItemTypeEpisode {
				episodeCount++
			}
		}

		seasonTotal, err := s.itemTypeCount(ctx, seasonID, storage.ItemTypeEpisode)
		if err != nil {
			return nil, err
		}

		// docs/12: "more than one episode landed in the window, or the
		// count landed in the window happens to equal that season's total
		// episode count (i.e. even a single episode counts as bulk when
		// it's the entirety of a short/complete season)".
		bulk := episodeCount > 1 || (episodeCount > 0 && episodeCount == seasonTotal)
		if bulk {
			seasonsInSeries, err := s.itemTypeCount(ctx, g.seriesID, storage.ItemTypeSeason)
			if err != nil {
				return nil, err
			}
			repID := g.seriesID
			if seasonsInSeries > 1 {
				repID = seasonID
			}
			item, err := s.store.GetItem(ctx, repID)
			if err != nil {
				return nil, nil
			}
			return &item, nil
		}

	default:
		// docs/12: "If the window's items span more than one season, the
		// card represents the series."
		item, err := s.store.GetItem(ctx, g.seriesID)
		if err != nil {
			return nil, nil
		}
		return &item, nil
	}

	latest := latestItemInGroup(g)
	return &latest, nil
}

// isSeriesFullyWatched reports whether every episode of seriesID is played
// (PlayCount > 0) for userID — confirmed live against the reference server
// (docs/12's "Implementation findings"): a 4-season series with every
// episode's Played set true for the requesting user never appeared in the
// grouped GET /Items/Latest result, at any limit large enough to otherwise
// include it, while a series ranked just behind it by recency but with at
// least one unwatched episode took its place. Checked across the whole
// series, not just the Episode/Season rows that happen to have landed in
// tvShowsLatestGrouped's candidate pool — confirmed live: marking every
// episode of a series played still excluded it even though only a few of
// its episodes were recent enough to be in that pool.
//
// "Played" reuses nextUpCandidateForSeries' PlayCount > 0 criterion
// (nextup.go, same package) rather than the Played bool: GetPlaystate's own
// zero-value convention (sqlite/playstate.go) means an episode with no
// playstate row at all — never touched — already reads back PlayCount 0,
// so "never watched" and "explicitly not played" both correctly fail this
// check with no special-casing needed.
//
// A series with zero episodes (defensive only: every group passed here was
// built from at least one Episode/Season candidate row, so its series has
// at least one) reports false — nothing to hide.
func (s *Server) isSeriesFullyWatched(ctx context.Context, userID storage.UserID, seriesID storage.ItemID) (bool, error) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode},
		SeriesID:  &seriesID,
		Limit:     nextUpEpisodeScanLimit,
	})
	if err != nil {
		return false, err
	}
	if len(page.Items) == 0 {
		return false, nil
	}
	for _, ep := range page.Items {
		st, err := s.store.GetPlaystate(ctx, userID, ep.ID)
		if err != nil {
			return false, err
		}
		if st.PlayCount <= 0 {
			return false, nil
		}
	}
	return true, nil
}

// tvShowsLatestGrouped implements docs/12's "Items/Latest — series grouping"
// for a TV-shows library: at most one card per series, chosen by
// pickSeriesCard, newest-series-addition first, truncated to limit groups —
// limit counts groups (series), not raw episodes (docs/12 step 2). A series
// that isSeriesFullyWatched reports as fully watched for userID is skipped
// entirely rather than counted against limit — confirmed live, see that
// function's doc comment — so it never displaces an otherwise-lower-ranked
// series that still has something unwatched.
func (s *Server) tvShowsLatestGrouped(ctx context.Context, libID storage.LibraryID, limit int, userID storage.UserID) ([]storage.Item, error) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		LibraryID: &libID,
		Recursive: true,
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode, storage.ItemTypeSeason},
		SortBy:    []storage.SortField{{By: storage.SortByDateCreated, Descending: true}},
		Limit:     latestSeriesGroupingPoolSize(limit),
	})
	if err != nil {
		return nil, err
	}

	ranked := rankSeriesGroups(groupBySeries(page.Items))

	cards := make([]storage.Item, 0, limit)
	for _, g := range ranked {
		if len(cards) >= limit {
			break
		}

		fullyWatched, err := s.isSeriesFullyWatched(ctx, userID, g.seriesID)
		if err != nil {
			return nil, err
		}
		if fullyWatched {
			continue
		}

		card, err := s.pickSeriesCard(ctx, g)
		if err != nil {
			return nil, err
		}
		if card != nil {
			cards = append(cards, *card)
		}
	}
	return cards, nil
}
