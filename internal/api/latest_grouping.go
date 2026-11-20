// latest_grouping.go implements the TV-shows branch of GET /Items/Latest's
// grouping behavior: docs/12-clean-room-behavior-specs.md's "Items/Latest —
// series grouping" section, produced under the clean-room process
// (AGENTS.md's "Clean-room requirement", D12) — this file was written from
// that spec plus black-box verification against a running reference server
// (reference server), never with the reference server's own source open.
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
// fullyWatched and its use in tvShowsLatestGrouped below.
package api

import (
	"context"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// seriesLatestWindowHours is docs/12's "24-hour lookback from that series'
// latest timestamp" — the trailing window used to decide which of a
// series' recently-added items belong to the same "recent addition event"
// for card-selection purposes.
const seriesLatestWindowHours = 24

// latestSeriesPoolMin/Multiplier/Max bound the single QuerySeriesLatestActivity
// call tvShowsLatestGrouped issues: enough distinct series ranked ahead of
// the request's own `limit` to absorb fully-watched skips, but still
// one bounded query — never the whole catalog (AGENTS.md principle 1, "idle
// memory is a budget"). Unlike this file's previous raw-item-count-bounded
// pool, this bound is in units of *series*, not episodes, so a handful of
// prolific series can no longer crowd a lower-episode-count-but-equally-recent
// series out of the ranking (storage.SeriesActivity's doc comment has the
// live case that surfaced this).
const (
	latestSeriesPoolMin        = 50
	latestSeriesPoolMultiplier = 5
	latestSeriesPoolMax        = 300
)

func latestSeriesPoolSize(limit int) int {
	n := limit * latestSeriesPoolMultiplier
	if n < latestSeriesPoolMin {
		n = latestSeriesPoolMin
	}
	if n > latestSeriesPoolMax {
		n = latestSeriesPoolMax
	}
	return n
}

// latestSeriesItemFetchLimit bounds the per-series item fetch
// pickSeriesCard's caller issues to build one series' candidate window
// (seriesLatestWindowHours' 24-hour trailing slice of that series' own
// Episode/Season rows) — generous enough that even a large single-day bulk
// import of one series lands entirely inside it, while still a single
// bounded per-series query rather than an unbounded scan.
const latestSeriesItemFetchLimit = 1000

// seriesGroup accumulates one series' recent Episode/Season rows (already
// scoped to that one series by the caller's SeriesID-filtered query), plus
// that series' own most recent addition timestamp (docs/12 step 1: "for
// each series, note the most recent addition timestamp in that series") —
// carried through separately from items because it comes from
// QuerySeriesLatestActivity, the authoritative per-series ranking source,
// rather than being recomputed from whatever happens to be in items.
type seriesGroup struct {
	seriesID storage.ItemID
	items    []storage.Item
	latest   time.Time
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

// fullyWatched reports whether every episode of a series is played
// (PlayCount > 0) for the requesting user — confirmed live against the
// reference server (docs/12's "Implementation findings"): a 4-season series
// with every episode's Played set true never appeared in the grouped
// GET /Items/Latest result, at any limit large enough to otherwise include it,
// while a series ranked just behind it by recency but with at least one
// unwatched episode took its place. Checked across the whole series, not just
// the Episode/Season rows that happen to have landed in the candidate pool.
//
// A series with zero episodes reports false — nothing to hide.
//
// The tally comes from one grouped query (storage.SeriesWatchStates) instead of
// a query per episode: the per-episode walk this replaced cost one round trip
// for every episode of every candidate series, and measured 8.3 to 9.3 seconds
// on a library with 86 series.
func fullyWatched(state storage.SeriesWatchState) bool {
	return state.Episodes > 0 && state.Unwatched == 0
}

// tvShowsLatestGrouped implements docs/12's "Items/Latest — series grouping"
// for a TV-shows library: at most one card per series, chosen by
// pickSeriesCard, newest-series-addition first, truncated to limit groups —
// limit counts groups (series), not raw episodes (docs/12 step 2). A series
// that fullyWatched reports as fully watched for userID is skipped
// entirely rather than counted against limit — confirmed live, see that
// function's doc comment — so it never displaces an otherwise-lower-ranked
// series that still has something unwatched.
func (s *Server) tvShowsLatestGrouped(ctx context.Context, libID storage.LibraryID, limit int, userID storage.UserID) ([]latestCard, error) {
	ranked, err := s.store.QuerySeriesLatestActivity(ctx, libID,
		[]storage.ItemType{storage.ItemTypeEpisode, storage.ItemTypeSeason},
		latestSeriesPoolSize(limit))
	if err != nil {
		return nil, err
	}

	// One query answers "which of these series still has something unwatched"
	// for the whole pool. Asking per series meant a query per episode — a
	// measured 8.3 to 9.3 seconds on a 86-series library.
	poolIDs := make([]storage.ItemID, 0, len(ranked))
	for _, activity := range ranked {
		poolIDs = append(poolIDs, activity.SeriesID)
	}
	states, err := s.store.SeriesWatchStates(ctx, userID, poolIDs)
	if err != nil {
		return nil, err
	}

	cards := make([]latestCard, 0, limit)
	for _, activity := range ranked {
		if len(cards) >= limit {
			break
		}

		if fullyWatched(states[activity.SeriesID]) {
			continue
		}

		// The window is pushed into the query: a series only contributes the
		// rows of its last 24 hours, not the thousand it ever had.
		windowStart := activity.Latest.Add(-seriesLatestWindowHours * time.Hour)
		page, err := s.store.QueryItems(ctx, storage.ItemFilter{
			SeriesID:         &activity.SeriesID,
			ItemTypes:        []storage.ItemType{storage.ItemTypeEpisode, storage.ItemTypeSeason},
			DateCreatedAfter: &windowStart,
			SortBy:           []storage.SortField{{By: storage.SortByDateCreated, Descending: true}},
			Limit:            latestSeriesItemFetchLimit,
		})
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			// Defensive only: QuerySeriesLatestActivity's own candidate rows
			// are the same Episode/Season types being re-fetched here, so
			// this series should always have at least one. Skip rather than
			// crash over a data anomaly (e.g. deleted between the two
			// queries).
			continue
		}
		g := &seriesGroup{seriesID: activity.SeriesID, items: page.Items, latest: activity.Latest}

		card, err := s.pickSeriesCard(ctx, g)
		if err != nil {
			return nil, err
		}
		if card != nil {
			cards = append(cards, latestCard{item: *card, latest: activity.Latest})
		}
	}
	return cards, nil
}
