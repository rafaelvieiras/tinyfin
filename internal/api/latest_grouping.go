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

// seriesPick is one candidate series' resolved representative before the items
// themselves are loaded: which item stands for the series, and the series' own
// latest addition timestamp (carried for latestCard's ordering).
type seriesPick struct {
	repID  storage.ItemID
	latest time.Time
}

// singleSeasonOf reports the one season key g's window touches, when it touches
// exactly one — the branch of docs/12 step 3 that needs that season's episode
// total. It is a pure read of g.items so the caller can decide, before asking
// for any count, which seasons the batched count query has to cover.
func singleSeasonOf(g *seriesGroup) (storage.ItemID, bool) {
	windowStart := g.latest.Add(-seriesLatestWindowHours * time.Hour)
	var one storage.ItemID
	seen := false
	for _, it := range g.items {
		if it.DateCreated.Before(windowStart) {
			continue
		}
		sid, ok := seasonKeyOf(it)
		if !ok {
			continue
		}
		if !seen {
			one, seen = sid, true
			continue
		}
		if sid != one {
			return "", false
		}
	}
	return one, seen
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

// representativeOf resolves docs/12 step 3 for one series group: which single
// item — a Season, the Series itself, or one Episode — represents it in the
// final result.
//
// It is deliberately pure. The rule needs two tallies (the season's episode
// total and the series' season total), and asking for them here is what made
// the row cost two queries per card; they now arrive from one batched query
// each, through the two lookups below. A tally of zero (a season with no
// episode, which cannot happen for a season that just received one) keeps the
// pre-batching behaviour.
//
// docs/12 step 4 (falling back to the single latest episode when the chosen
// entity fails a per-user visibility check) is intentionally not implemented:
// Tinyfin has no parental/user-visibility concept yet (internal/api/users.go's
// confirmed "every user is public" note), so that check is a no-op today.
func representativeOf(g *seriesGroup, episodeTotalOf, seasonTotalOf func(storage.ItemID) int) storage.ItemID {
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

		// docs/12: "more than one episode landed in the window, or the count
		// landed in the window happens to equal that season's total episode
		// count (i.e. even a single episode counts as bulk when it's the
		// entirety of a short/complete season)".
		bulk := episodeCount > 1 || (episodeCount > 0 && episodeCount == episodeTotalOf(seasonID))
		if bulk {
			if seasonTotalOf(g.seriesID) > 1 {
				return seasonID
			}
			return g.seriesID
		}

	default:
		// docs/12: "If the window's items span more than one season, the card
		// represents the series."
		return g.seriesID
	}

	return latestItemInGroup(g).ID
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
// representativeOf, newest-series-addition first, truncated to limit groups —
// limit counts groups (series), not raw episodes (docs/12 step 2). A series
// that fullyWatched reports as fully watched for userID is skipped entirely
// rather than counted against limit — confirmed live, see that function's doc
// comment — so it never displaces an otherwise-lower-ranked series that still
// has something unwatched.
//
// The whole row costs six store reads, whatever the pool holds: the ranking, the
// candidates' windows, their watch states, their season episode totals, their
// season totals and one fetch of the picked items. It used to be four reads per
// card — the window plus three counts — measured at 30 ms per card on a
// 6,187-item library, which is why a 16-card row cost 565 ms while the ranking
// query itself took 16 ms (docs/15-response-cache-analysis.md).
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

	// The pool exists to absorb fully-watched skips. Every candidate that
	// survives that filter yields exactly one card, so only the first `limit`
	// of them can reach the answer: the window query below is scoped to those,
	// not to the whole pool.
	candidates := make([]storage.SeriesActivity, 0, limit)
	for _, activity := range ranked {
		if len(candidates) >= limit {
			break
		}
		if fullyWatched(states[activity.SeriesID]) {
			continue
		}
		candidates = append(candidates, activity)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// One query for every candidate's 24-hour window (docs/12 step 1), instead
	// of one query per candidate.
	windows := make([]storage.SeriesWindow, 0, len(candidates))
	for _, activity := range candidates {
		windows = append(windows, storage.SeriesWindow{
			SeriesID: activity.SeriesID,
			Since:    activity.Latest.Add(-seriesLatestWindowHours * time.Hour),
		})
	}
	recent, err := s.store.SeriesRecentItems(ctx, windows,
		[]storage.ItemType{storage.ItemTypeEpisode, storage.ItemTypeSeason}, latestSeriesItemFetchLimit)
	if err != nil {
		return nil, err
	}

	groups := make([]*seriesGroup, 0, len(candidates))
	seasonParents := make([]storage.ItemID, 0, len(candidates))
	seriesParents := make([]storage.ItemID, 0, len(candidates))
	for _, activity := range candidates {
		items := recent[activity.SeriesID]
		if len(items) == 0 {
			// Defensive only: the ranking's own candidate rows are the same
			// Episode/Season types fetched here, so a candidate should always
			// have at least one. Skip rather than crash over a data anomaly
			// (e.g. deleted between the two queries).
			continue
		}
		g := &seriesGroup{seriesID: activity.SeriesID, items: items, latest: activity.Latest}
		groups = append(groups, g)
		if seasonID, ok := singleSeasonOf(g); ok {
			seasonParents = append(seasonParents, seasonID)
			seriesParents = append(seriesParents, activity.SeriesID)
		}
	}

	// The two tallies representativeOf's bulk rule reads, one query each for
	// the whole row: how many episodes that season holds, and how many seasons
	// the series holds.
	episodeTotals, err := s.store.ChildItemCounts(ctx, seasonParents, storage.ItemTypeEpisode)
	if err != nil {
		return nil, err
	}
	seasonTotals, err := s.store.ChildItemCounts(ctx, seriesParents, storage.ItemTypeSeason)
	if err != nil {
		return nil, err
	}

	picks := make([]seriesPick, 0, limit)
	for _, g := range groups {
		if len(picks) >= limit {
			break
		}
		picks = append(picks, seriesPick{
			repID: representativeOf(g, func(id storage.ItemID) int { return episodeTotals[id] },
				func(id storage.ItemID) int { return seasonTotals[id] }),
			latest: g.latest,
		})
	}

	// One fetch for every picked item, ordered by our own list.
	ids := make([]storage.ItemID, 0, len(picks))
	for _, p := range picks {
		ids = append(ids, p.repID)
	}
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{IDs: ids, OrderByIDs: true, Limit: len(ids)})
	if err != nil {
		return nil, err
	}
	byID := make(map[storage.ItemID]storage.Item, len(page.Items))
	for _, it := range page.Items {
		byID[it.ID] = it
	}

	cards := make([]latestCard, 0, len(picks))
	for _, p := range picks {
		item, ok := byID[p.repID]
		if !ok {
			// Deleted between the count queries and this fetch: skip the card
			// rather than failing the whole row, same as before.
			continue
		}
		cards = append(cards, latestCard{item: item, latest: p.latest})
	}
	return cards, nil
}
