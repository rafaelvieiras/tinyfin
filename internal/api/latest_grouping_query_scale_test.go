package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// countingStore decorates a real store and counts the read calls one request
// makes, so a test can assert how a handler's cost scales with the size of its
// candidate pool instead of asserting a wall-clock time.
//
// It exists because the TV-shows branch of GET /Items/Latest was measured at
// 592 ms on a 6,187-item library (docs/15-response-cache-analysis.md), and the
// cost of that row was not the catalog: it was one QueryItems plus one
// itemTypeCount per series in the pool — 80 series for a limit of 16, so ~320
// statements per request. This file pins the property those numbers broke: one
// grouped row must not ask a question per series.
type countingStore struct {
	storage.Store
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingStore) hit(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[name]++
}

// reset drops the tally so a caller can count one request in isolation.
func (c *countingStore) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = map[string]int{}
}

// total is every counted read call since the last reset.
func (c *countingStore) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

func (c *countingStore) tally() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.calls))
	for k, v := range c.calls {
		out[k] = v
	}
	return out
}

func (c *countingStore) QueryItems(ctx context.Context, filter storage.ItemFilter) (storage.ItemPage, error) {
	c.hit("QueryItems")
	return c.Store.QueryItems(ctx, filter)
}

func (c *countingStore) GetItem(ctx context.Context, id storage.ItemID) (storage.Item, error) {
	c.hit("GetItem")
	return c.Store.GetItem(ctx, id)
}

func (c *countingStore) QuerySeriesLatestActivity(ctx context.Context, libraryID storage.LibraryID, itemTypes []storage.ItemType, limit int) ([]storage.SeriesActivity, error) {
	c.hit("QuerySeriesLatestActivity")
	return c.Store.QuerySeriesLatestActivity(ctx, libraryID, itemTypes, limit)
}

func (c *countingStore) SeriesWatchStates(ctx context.Context, userID storage.UserID, seriesIDs []storage.ItemID) (map[storage.ItemID]storage.SeriesWatchState, error) {
	c.hit("SeriesWatchStates")
	return c.Store.SeriesWatchStates(ctx, userID, seriesIDs)
}

func (c *countingStore) SeriesRecentItems(ctx context.Context, windows []storage.SeriesWindow, itemTypes []storage.ItemType, perSeriesLimit int) (map[storage.ItemID][]storage.Item, error) {
	c.hit("SeriesRecentItems")
	return c.Store.SeriesRecentItems(ctx, windows, itemTypes, perSeriesLimit)
}

func (c *countingStore) ChildItemCounts(ctx context.Context, parentIDs []storage.ItemID, itemType storage.ItemType) (map[storage.ItemID]int, error) {
	c.hit("ChildItemCounts")
	return c.Store.ChildItemCounts(ctx, parentIDs, itemType)
}

func (c *countingStore) ImageRefsFor(ctx context.Context, itemIDs []storage.ItemID, kinds []storage.ImageKind) (map[storage.ItemID]map[storage.ImageKind][]storage.ImageRef, error) {
	c.hit("ImageRefsFor")
	return c.Store.ImageRefsFor(ctx, itemIDs, kinds)
}

func (c *countingStore) PlaystatesFor(ctx context.Context, userID storage.UserID, itemIDs []storage.ItemID) (map[storage.ItemID]storage.Playstate, error) {
	c.hit("PlaystatesFor")
	return c.Store.PlaystatesFor(ctx, userID, itemIDs)
}

func (c *countingStore) LeafCountsFor(ctx context.Context, userID storage.UserID, folderIDs []storage.ItemID) (map[storage.ItemID]storage.LeafCount, error) {
	c.hit("LeafCountsFor")
	return c.Store.LeafCountsFor(ctx, userID, folderIDs)
}

func (c *countingStore) ChildCountsFor(ctx context.Context, parentIDs []storage.ItemID) (map[storage.ItemID]int, error) {
	c.hit("ChildCountsFor")
	return c.Store.ChildCountsFor(ctx, parentIDs)
}

func (c *countingStore) OwnedExtraCountsFor(ctx context.Context, ownerIDs []storage.ItemID, extraType string) (map[storage.ItemID]int, error) {
	c.hit("OwnedExtraCountsFor")
	return c.Store.OwnedExtraCountsFor(ctx, ownerIDs, extraType)
}

func (c *countingStore) RecursiveItemCountsFor(ctx context.Context, parentIDs []storage.ItemID, excludeTypes []storage.ItemType) (map[storage.ItemID]int, error) {
	c.hit("RecursiveItemCountsFor")
	return c.Store.RecursiveItemCountsFor(ctx, parentIDs, excludeTypes)
}

func (c *countingStore) GetPlaystate(ctx context.Context, userID storage.UserID, itemID storage.ItemID) (storage.Playstate, error) {
	c.hit("GetPlaystate")
	return c.Store.GetPlaystate(ctx, userID, itemID)
}

func (c *countingStore) GetImageRefs(ctx context.Context, itemID storage.ItemID, kind storage.ImageKind) ([]storage.ImageRef, error) {
	c.hit("GetImageRefs")
	return c.Store.GetImageRefs(ctx, itemID, kind)
}

func (c *countingStore) FolderLeafCounts(ctx context.Context, userID storage.UserID, folderID storage.ItemID) (int, int, error) {
	c.hit("FolderLeafCounts")
	return c.Store.FolderLeafCounts(ctx, userID, folderID)
}

// openTestStore opens a fresh sqlite store in the test's temp dir.
func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

// startTestServerWithStore runs a server over store and returns its httptest
// instance — newTestServer's counterpart for a decorated store.
func startTestServerWithStore(t *testing.T, store storage.Store) *httptest.Server {
	t.Helper()
	srv := startTestServer(t, store, Config{ServerName: "TestServer"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// lookupItemID resolves a seeded item's minted id by name. It exists instead of
// latest_grouping_test.go's mustFindItemByName because that helper reads a
// 100-item page, and the pools this test builds are larger than that.
func lookupItemID(t *testing.T, st storage.ItemStore, libID storage.LibraryID, name string) storage.ItemID {
	t.Helper()
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Recursive: true, Limit: 5000})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	for _, it := range page.Items {
		if it.Name == name {
			return it.ID
		}
	}
	t.Fatalf("no item named %q in library %q", name, libID)
	return ""
}

// seedSeriesBurst inserts n series into libID, each with one season holding two
// episodes inside the grouping window.
func seedSeriesBurst(t *testing.T, st storage.ItemStore, libID storage.LibraryID, n int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	upsert := func(item storage.Item, name string) storage.ItemID {
		if err := st.UpsertItem(ctx, item); err != nil {
			t.Fatalf("UpsertItem %q: %v", name, err)
		}
		return lookupItemID(t, st, libID, name)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Pool Show %02d", i)
		seriesID := upsert(storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: name,
			Path: "/media/series/" + name, DateCreated: now.Add(-2 * time.Hour)}, name)

		seasonName := name + " Season 1"
		idx := 1
		seasonID := upsert(storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID,
			Name: seasonName, IndexNumber: &idx, Path: "/media/series/" + seasonName, DateCreated: now.Add(-90 * time.Minute)}, seasonName)

		for ep := 1; ep <= 2; ep++ {
			epName := fmt.Sprintf("%s S01E%02d", name, ep)
			epIdx := ep
			upsert(storage.Item{Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seasonID, SeriesID: &seriesID,
				SeasonID: &seasonID, Name: epName, IndexNumber: &epIdx, Path: "/media/series/" + epName + ".mkv",
				DateCreated: now.Add(time.Duration(-80+ep*10) * time.Minute)}, epName)
		}
	}
}

// The grouping path's own budget: four answered reads whatever the pool holds
// (the ranking, the candidates' windows, their watch states, and the two
// batched tallies plus the one fetch of the picked items — six calls in total,
// counted here as four named plus QueryItems' bounded share below).
const maxFixedGroupedRowCalls = 6

// dtoPerItemCalls are the calls baseItemDto used to make once per card: its
// picture refs (six single-instance kinds plus Backdrop), the caller's playstate,
// a folder's leaf counts, the two counts, and one lookup per ancestor. The row
// is now answered without any of them, which is what makes its statement count
// independent of the number of cards.
var dtoPerItemCalls = []string{"GetImageRefs", "GetPlaystate", "FolderLeafCounts", "GetItem"}

// dtoBatchedCalls are the page-wide answers s.prefetchDTOs issues for a
// Series/Season row, each exactly once.
var dtoBatchedCalls = []string{"ImageRefsFor", "PlaystatesFor", "LeafCountsFor", "ChildCountsFor", "OwnedExtraCountsFor", "RecursiveItemCountsFor"}

// maxGroupedRowCalls is the whole row's statement budget, and it is a constant:
// the grouping path's fixed reads, the DTO's page-wide reads, and the two
// bounded fetches (the picked items and their ancestors). It does not grow with
// the pool and it does not grow with the limit — that is the property the two
// tests below assert.
const maxGroupedRowCalls = 13

// TestItemsLatestSeriesGroupingCostDoesNotGrowWithThePool is the regression
// guard for the second N+1 found while re-measuring docs/15: one grouped row
// asked four store reads per card — the candidate's window plus three counts —
// measured at 30 ms per card on a 6,187-item production library, so a 16-card
// row cost 565 ms while the ranking query itself took 16 ms.
//
// Two assertions, both structural:
//
//  1. the grouping path answers with a fixed number of reads, whatever the pool
//     holds;
//  2. the whole row's cost is the same for a pool of 12 series and a pool of 60.
func TestItemsLatestSeriesGroupingCostDoesNotGrowWithThePool(t *testing.T) {
	const wantLimit = 5
	pools := []int{12, 60}
	costs := make([]int, len(pools))
	tallies := make([]map[string]int, len(pools))

	for i, pool := range pools {
		st := openTestStore(t)
		cs := &countingStore{Store: st}
		ts := startTestServerWithStore(t, cs)

		user := createTestUser(t, st, "dave", "davepass")
		token := loginToken(t, ts.URL, user.Name, "davepass")
		get := authedClient(t, ts.URL, token)

		libID := createTestLibrary(t, st, fmt.Sprintf("Pool Shows %02d", pool), storage.CollectionTypeTVShows)
		seedSeriesBurst(t, st, libID, pool)

		// One warm-up request keeps first-use work (schema checks, statement
		// preparation) out of the tally.
		if got := latestGrouped(t, get, libID, fmt.Sprintf("&limit=%d", wantLimit)); len(got) != wantLimit {
			t.Fatalf("warm-up Items/Latest over %d series returned %d cards, want %d", pool, len(got), wantLimit)
		}

		cs.reset()
		items := latestGrouped(t, get, libID, fmt.Sprintf("&limit=%d", wantLimit))
		if len(items) != wantLimit {
			t.Fatalf("Items/Latest over %d series returned %d cards, want %d", pool, len(items), wantLimit)
		}
		costs[i] = cs.total()
		tallies[i] = cs.tally()
	}

	for i, pool := range pools {
		tally := tallies[i]
		fixed := tally["QuerySeriesLatestActivity"] + tally["SeriesWatchStates"] +
			tally["SeriesRecentItems"] + tally["ChildItemCounts"]
		if fixed > maxFixedGroupedRowCalls {
			t.Errorf("pool of %d series: the grouping path took %d store reads (%v), want at most %d",
				pool, fixed, tally, maxFixedGroupedRowCalls)
		}
		for _, name := range []string{"QuerySeriesLatestActivity", "SeriesWatchStates", "SeriesRecentItems"} {
			if tally[name] != 1 {
				t.Errorf("pool of %d series: %s called %d times, want exactly 1 (%v)", pool, name, tally[name], tally)
			}
		}
		if tally["ChildItemCounts"] != 2 {
			t.Errorf("pool of %d series: ChildItemCounts called %d times, want exactly 2 — the season's episode total and the series' season total (%v)",
				pool, tally["ChildItemCounts"], tally)
		}
		if tally["GetItem"] != 0 {
			t.Errorf("pool of %d series: GetItem called %d times, want 0 — the picked items come from one fetch (%v)",
				pool, tally["GetItem"], tally)
		}
		for _, name := range dtoPerItemCalls {
			if tally[name] != 0 {
				t.Errorf("pool of %d series: %s called %d times, want 0 — the DTO's per-card questions are answered for the page (%v)",
					pool, name, tally[name], tally)
			}
		}
		for _, name := range dtoBatchedCalls {
			if tally[name] != 1 {
				t.Errorf("pool of %d series: %s called %d times, want exactly 1 (%v)", pool, name, tally[name], tally)
			}
		}
		if costs[i] > maxGroupedRowCalls {
			t.Errorf("pool of %d series: the row cost %d store reads (%v), want at most %d",
				pool, costs[i], tally, maxGroupedRowCalls)
		}
	}

	if costs[0] != costs[1] {
		t.Errorf("cost depends on the pool size: %d series cost %d read calls (%v), %d series cost %d (%v)",
			pools[0], costs[0], tallies[0], pools[1], costs[1], tallies[1])
	}
}

// TestItemsLatestSeriesGroupingCostDoesNotGrowWithTheLimit asserts the other
// half of the same property: the row's statement count does not grow with the
// number of cards either. A page of 4 cards and a page of 16 cost the same,
// because every per-card question is answered for the page in one statement.
//
// It is a different failure from a per-series loop: a limit-driven regression
// here means the DTO went back to asking the store one question per item.
func TestItemsLatestSeriesGroupingCostDoesNotGrowWithTheLimit(t *testing.T) {
	const pool = 30
	limits := []int{4, 16}
	costs := make([]int, len(limits))
	tallies := make([]map[string]int, len(limits))

	for i, limit := range limits {
		st := openTestStore(t)
		cs := &countingStore{Store: st}
		ts := startTestServerWithStore(t, cs)

		user := createTestUser(t, st, "erin", "erinpass")
		token := loginToken(t, ts.URL, user.Name, "erinpass")
		get := authedClient(t, ts.URL, token)

		libID := createTestLibrary(t, st, fmt.Sprintf("Limit Shows %02d", limit), storage.CollectionTypeTVShows)
		seedSeriesBurst(t, st, libID, pool)

		if got := latestGrouped(t, get, libID, fmt.Sprintf("&limit=%d", limit)); len(got) != limit {
			t.Fatalf("warm-up Items/Latest limit=%d over %d series returned %d cards", limit, pool, len(got))
		}
		cs.reset()
		if got := latestGrouped(t, get, libID, fmt.Sprintf("&limit=%d", limit)); len(got) != limit {
			t.Fatalf("Items/Latest limit=%d over %d series returned %d cards", limit, pool, len(got))
		}
		costs[i] = cs.total()
		tallies[i] = cs.tally()
	}

	for i, limit := range limits {
		for _, name := range dtoPerItemCalls {
			if tallies[i][name] != 0 {
				t.Errorf("limit=%d: %s called %d times, want 0 (%v)", limit, name, tallies[i][name], tallies[i])
			}
		}
	}
	if costs[0] != costs[1] {
		t.Errorf("cost depends on the limit: %d cards cost %d store reads (%v), %d cards cost %d (%v)",
			limits[0], costs[0], tallies[0], limits[1], costs[1], tallies[1])
	}
}

// TestItemsLatestSeriesGroupingCountsIgnoreExtras pins the batched counts'
// parity with the per-parent QueryItems they replaced: that path excludes
// extras (items.extra_type <> ”, storage's "extras belong to the item that owns
// them, not to the catalog"), and so must SeriesRecentItems and
// ChildItemCounts.
//
// The fixture makes the difference visible: one real episode plus one extra in
// the window. The real episode is the season's entire episode count, so the
// bulk rule fires and the card is the SEASON — unless the extra is counted in
// either tally, in which case the window holds 1 of 2 and the card falls back to
// the single episode.
func TestItemsLatestSeriesGroupingCountsIgnoreExtras(t *testing.T) {
	st := openTestStore(t)
	ts := startTestServerWithStore(t, st)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Extras", storage.CollectionTypeTVShows)
	now := time.Now().UTC()

	seriesID := upsertSeries(t, st, libID, "Extra Show", now.Add(-100*24*time.Hour))
	season1ID := upsertSeason(t, st, libID, seriesID, "Extra Show Season 1", 1, now.Add(-100*24*time.Hour))
	upsertEpisode(t, st, libID, seriesID, season1ID, "ES01E01", 1, now.Add(-100*24*time.Hour))
	season2ID := upsertSeason(t, st, libID, seriesID, "Extra Show Season 2", 2, now.Add(-50*time.Minute))
	upsertEpisode(t, st, libID, seriesID, season2ID, "ES02E01", 1, now.Add(-40*time.Minute))

	extraName := "ES02E01 featurette"
	idx := 2
	owner := season2ID
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &season2ID, SeriesID: &seriesID,
		SeasonID: &season2ID, Name: extraName, IndexNumber: &idx, Path: "/media/series/" + extraName + ".mkv",
		DateCreated: now.Add(-35 * time.Minute), ExtraType: storage.ExtraTypeFeaturette, OwnerID: &owner,
	}); err != nil {
		t.Fatalf("UpsertItem extra: %v", err)
	}

	items := latestGrouped(t, get, libID, "")
	if len(items) != 1 {
		t.Fatalf("Items/Latest = %+v, want exactly 1 card", items)
	}
	if items[0].Type != "Season" || items[0].ID != string(season2ID) {
		t.Errorf("card = %q %q, want Season %q: the extra must not count as an episode", items[0].Type, items[0].ID, season2ID)
	}
}
