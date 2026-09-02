package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// upsertSeries/upsertSeason/upsertEpisode are small test-only helpers that
// wrap st.UpsertItem for the three TV item types latest_grouping.go cares
// about, letting each scenario below set an explicit DateCreated (the
// grouping window/bulk rule, docs/12, is entirely driven by that field) and
// look the minted ID back up via mustFindItemByName immediately after.

func upsertSeries(t *testing.T, st storage.ItemStore, libID storage.LibraryID, name string, dateCreated time.Time) storage.ItemID {
	t.Helper()
	item := storage.Item{
		Type: storage.ItemTypeSeries, LibraryID: libID, Name: name,
		Path: "/media/series/" + name, DateCreated: dateCreated,
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem series %q: %v", name, err)
	}
	return mustFindItemByName(t, st, libID, name)
}

func upsertSeason(t *testing.T, st storage.ItemStore, libID storage.LibraryID, seriesID storage.ItemID, name string, index int, dateCreated time.Time) storage.ItemID {
	t.Helper()
	idx := index
	item := storage.Item{
		Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID,
		Name: name, IndexNumber: &idx, Path: "/media/series/" + name, DateCreated: dateCreated,
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem season %q: %v", name, err)
	}
	return mustFindItemByName(t, st, libID, name)
}

func upsertEpisode(t *testing.T, st storage.ItemStore, libID storage.LibraryID, seriesID, seasonID storage.ItemID, name string, index int, dateCreated time.Time) storage.ItemID {
	t.Helper()
	idx := index
	item := storage.Item{
		Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
		Name: name, IndexNumber: &idx, Path: "/media/series/" + name + ".mkv", DateCreated: dateCreated,
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem episode %q: %v", name, err)
	}
	return mustFindItemByName(t, st, libID, name)
}

// latestGrouped issues GET /Items/Latest?parentId=<libID>[&limit=n] and
// decodes the bare BaseItemDto array handleItemsLatest returns.
func latestGrouped(t *testing.T, get func(method, path string) *http.Response, libID storage.LibraryID, extraQuery string) []baseItemDto {
	t.Helper()
	resp := get(http.MethodGet, "/Items/Latest?parentId="+string(libID)+extraQuery)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/Latest status = %d, want 200", resp.StatusCode)
	}
	var items []baseItemDto
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decoding /Items/Latest: %v", err)
	}
	return items
}

// TestItemsLatestSeriesGroupingBulkSingleSeason: several episodes landing in
// one season of a multi-season series within the 24h window collapse to a
// single card representing that SEASON (docs/12: "On a bulk addition, in a
// series that has more than one season total, the card represents the
// season").
func TestItemsLatestSeriesGroupingBulkSingleSeason(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Bulk Season Show", now.Add(-100*24*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Season 1", 1, now.Add(-100*24*time.Hour))
	season2ID := upsertSeason(t, st, libID, seriesID, "Season 2", 2, now.Add(-1*time.Hour))

	// Season 1: old episodes, well outside any 24h window relative to the
	// series' latest addition below — must never factor into the bulk
	// decision.
	upsertEpisode(t, st, libID, seriesID, season1ID, "BS1E1", 1, now.Add(-100*24*time.Hour))
	upsertEpisode(t, st, libID, seriesID, season1ID, "BS1E2", 2, now.Add(-100*24*time.Hour))

	// Season 2: three episodes landing together, minutes apart, well inside
	// the 24h window — a bulk single-season addition.
	upsertEpisode(t, st, libID, seriesID, season2ID, "BS2E1", 1, now.Add(-50*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season2ID, "BS2E2", 2, now.Add(-40*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season2ID, "BS2E3", 3, now.Add(-30*time.Minute))

	items := latestGrouped(t, get, libID, "")
	if len(items) != 1 {
		t.Fatalf("Items/Latest = %+v, want exactly 1 card", items)
	}
	if items[0].Type != "Season" || items[0].ID != string(season2ID) {
		t.Errorf("card = %+v, want Season %q", items[0], season2ID)
	}
}

// TestItemsLatestSeriesGroupingBulkMultiSeason: a burst that spans more than
// one season within the window collapses to the SERIES card instead (docs/12:
// "If the window's items span more than one season, the card represents the
// series").
func TestItemsLatestSeriesGroupingBulkMultiSeason(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Multi Season Burst Show", now.Add(-2*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Season 1", 1, now.Add(-2*time.Hour))
	season2ID := upsertSeason(t, st, libID, seriesID, "Season 2", 2, now.Add(-2*time.Hour))

	// Season 1's episode and Season 2's episode both land within the same
	// 24h window (minutes apart) — a burst spanning two seasons.
	upsertEpisode(t, st, libID, seriesID, season1ID, "MS1E1", 1, now.Add(-90*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season1ID, "MS1E2", 2, now.Add(-80*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season2ID, "MS2E1", 1, now.Add(-70*time.Minute))

	items := latestGrouped(t, get, libID, "")
	if len(items) != 1 {
		t.Fatalf("Items/Latest = %+v, want exactly 1 card", items)
	}
	if items[0].Type != "Series" || items[0].ID != string(seriesID) {
		t.Errorf("card = %+v, want Series %q", items[0], seriesID)
	}
}

// TestItemsLatestSeriesGroupingSingleEpisode: exactly one episode lands in
// the window, and it is not its season's only episode — the card is that
// single episode directly, per docs/12.
func TestItemsLatestSeriesGroupingSingleEpisode(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Single Episode Show", now.Add(-100*24*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Season 1", 1, now.Add(-100*24*time.Hour))

	// Older siblings in the same season, outside the 24h window — the
	// season has more than one episode total, so the new arrival alone
	// isn't "the entirety of a short/complete season" either.
	upsertEpisode(t, st, libID, seriesID, season1ID, "SE1", 1, now.Add(-100*24*time.Hour))
	upsertEpisode(t, st, libID, seriesID, season1ID, "SE2", 2, now.Add(-100*24*time.Hour))
	newEpID := upsertEpisode(t, st, libID, seriesID, season1ID, "SE3", 3, now.Add(-10*time.Minute))

	items := latestGrouped(t, get, libID, "")
	if len(items) != 1 {
		t.Fatalf("Items/Latest = %+v, want exactly 1 card", items)
	}
	if items[0].Type != "Episode" || items[0].ID != string(newEpID) {
		t.Errorf("card = %+v, want Episode %q", items[0], newEpID)
	}
}

// TestItemsLatestSeriesGroupingSingleSeasonShowBulk: a bulk addition in a
// series that only has one season total represents the SERIES, not the
// season — docs/12: "there's no useful 'season' distinction to show for a
// single-season show".
func TestItemsLatestSeriesGroupingSingleSeasonShowBulk(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Single Season Show", now.Add(-1*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Season 1", 1, now.Add(-1*time.Hour))

	upsertEpisode(t, st, libID, seriesID, season1ID, "OS1E1", 1, now.Add(-50*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season1ID, "OS1E2", 2, now.Add(-40*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season1ID, "OS1E3", 3, now.Add(-30*time.Minute))

	items := latestGrouped(t, get, libID, "")
	if len(items) != 1 {
		t.Fatalf("Items/Latest = %+v, want exactly 1 card", items)
	}
	if items[0].Type != "Series" || items[0].ID != string(seriesID) {
		t.Errorf("card = %+v, want Series %q (single-season show, still bulk)", items[0], seriesID)
	}
}

// TestItemsLatestMoviesNotGrouped confirms a movies-library request is
// entirely unaffected by the TV-shows grouping added here — docs/12:
// "Movies — confirmed, no grouping": every distinct movie is its own card.
func TestItemsLatestMoviesNotGrouped(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Filmes", storage.CollectionTypeMovies)
	now := time.Now().UTC()

	for i, name := range []string{"Movie A", "Movie B", "Movie C"} {
		item := storage.Item{
			Type: storage.ItemTypeMovie, LibraryID: libID, Name: name,
			Path: "/media/movies/" + name + ".mkv", DateCreated: now.Add(-time.Duration(i) * time.Minute),
		}
		if err := st.UpsertItem(context.Background(), item); err != nil {
			t.Fatalf("UpsertItem %q: %v", name, err)
		}
	}

	items := latestGrouped(t, get, libID, "")
	if len(items) != 3 {
		t.Fatalf("Items/Latest = %+v, want 3 ungrouped movie cards", items)
	}
	for _, it := range items {
		if it.Type != "Movie" {
			t.Errorf("card %+v: Type = %q, want Movie", it, it.Type)
		}
	}
}

// TestItemsLatestLimitCountsGroupsNotItems: docs/12 step 2, "limit counts
// series/groups, not raw episodes" — a bulk addition of many episodes in
// one series still only consumes one of the requested slots, and a small
// limit truncates by GROUP.
func TestItemsLatestLimitCountsGroupsNotItems(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	// Series A: a genuine bulk addition of 12 episodes across two seasons —
	// would be 12 rows in the old flat behavior, must be exactly one group
	// here (a Series-level card, multi-season burst).
	seriesAID := upsertSeries(t, st, libID, "Series A", now.Add(-30*time.Minute))
	aSeason1 := upsertSeason(t, st, libID, seriesAID, "A Season 1", 1, now.Add(-30*time.Minute))
	aSeason2 := upsertSeason(t, st, libID, seriesAID, "A Season 2", 2, now.Add(-30*time.Minute))
	for i := 1; i <= 6; i++ {
		upsertEpisode(t, st, libID, seriesAID, aSeason1, "AS1E"+itoa(i), i, now.Add(-time.Duration(30-i)*time.Minute))
	}
	for i := 1; i <= 6; i++ {
		upsertEpisode(t, st, libID, seriesAID, aSeason2, "AS2E"+itoa(i), i, now.Add(-time.Duration(24-i)*time.Minute))
	}

	// Series B: a single newer episode, but still older than Series A's own
	// most recent addition (-18min) above, so Series A ranks first.
	seriesBID := upsertSeries(t, st, libID, "Series B", now.Add(-100*24*time.Hour))
	bSeason1 := upsertSeason(t, st, libID, seriesBID, "B Season 1", 1, now.Add(-100*24*time.Hour))
	upsertEpisode(t, st, libID, seriesBID, bSeason1, "BS1E1", 1, now.Add(-100*24*time.Hour))
	upsertEpisode(t, st, libID, seriesBID, bSeason1, "BS1E2", 2, now.Add(-45*time.Minute))

	// Unbounded: both series' cards show up, one each.
	all := latestGrouped(t, get, libID, "")
	if len(all) != 2 {
		t.Fatalf("Items/Latest (no limit) = %+v, want 2 cards (one per series)", all)
	}
	if all[0].ID != string(seriesAID) {
		t.Errorf("all[0] = %+v, want Series A first (its own latest addition is newer)", all[0])
	}

	// limit=1: only the single newest GROUP, even though Series A alone
	// represents 12 underlying episode rows.
	oneOnly := latestGrouped(t, get, libID, "&limit=1")
	if len(oneOnly) != 1 {
		t.Fatalf("Items/Latest (limit=1) = %+v, want exactly 1 card", oneOnly)
	}
	if oneOnly[0].ID != string(seriesAID) {
		t.Errorf("Items/Latest (limit=1) = %+v, want Series A's card", oneOnly[0])
	}
}

// TestItemsLatestGroupItemsFalse confirms the groupItems=false escape hatch
// (docs/12's "What determines 'grouped at all'") returns the original flat,
// ungrouped item list even for a TV-shows library — every recently-scanned
// row (series, season, and episode alike, exactly what handleItemsLatest
// returned before this grouping existed), not collapsed to one card.
func TestItemsLatestGroupItemsFalse(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Ungrouped Show", now.Add(-1*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Season 1", 1, now.Add(-1*time.Hour))
	upsertEpisode(t, st, libID, seriesID, season1ID, "UE1", 1, now.Add(-50*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season1ID, "UE2", 2, now.Add(-40*time.Minute))

	// Grouped (the default): the whole burst collapses to one Series card,
	// confirming this fixture would otherwise group.
	grouped := latestGrouped(t, get, libID, "")
	if len(grouped) != 1 || grouped[0].Type != "Series" {
		t.Fatalf("Items/Latest (grouped) = %+v, want exactly 1 Series card", grouped)
	}

	// groupItems=false: back to one row per series/season/episode item —
	// 4 rows total (the series, its season, and the 2 episodes).
	items := latestGrouped(t, get, libID, "&groupItems=false")
	if len(items) != 4 {
		t.Fatalf("Items/Latest?groupItems=false = %+v, want the 4 flat rows", items)
	}
	types := map[string]int{}
	for _, it := range items {
		types[it.Type]++
	}
	if types["Episode"] != 2 || types["Season"] != 1 || types["Series"] != 1 {
		t.Errorf("Items/Latest?groupItems=false type counts = %+v, want 2 Episode + 1 Season + 1 Series", types)
	}
}

// itoa avoids pulling in strconv just for these small loop indices in test
// name construction above.
func itoa(n int) string {
	digits := "0123456789"
	if n < 10 {
		return string(digits[n])
	}
	return string(digits[n/10]) + string(digits[n%10])
}
