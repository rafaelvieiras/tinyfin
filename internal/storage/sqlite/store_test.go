package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tinyfin.db")
	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func TestOpenAndMigrate(t *testing.T) {
	st := openTestStore(t)
	if err := st.Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}

	var version int
	if err := st.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("reading schema_migrations: %v", err)
	}
	if version != 4 {
		t.Fatalf("expected schema version 4, got %d", version)
	}

	// Reopening an already-migrated database must be a no-op, not an error.
	path := filepath.Join(t.TempDir(), "reopen.db")
	first, err := New(path)
	if err != nil {
		t.Fatalf("New (first open): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	second, err := New(path)
	if err != nil {
		t.Fatalf("New (second open): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// seedTree creates one library and a series -> season -> episode chain,
// plus a sibling top-level movie directly under the library, to exercise
// ItemFilter.Recursive against the ancestor-closure table.
func seedTree(t *testing.T, st *Store) (libraryID storage.LibraryID, seriesID, seasonID, episodeID, movieID storage.ItemID) {
	t.Helper()
	ctx := context.Background()

	lib := storage.Library{
		Name:           "TV Shows",
		CollectionType: storage.CollectionTypeTVShows,
		Paths:          []string{"/media/tv"},
	}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	libs, err := st.QueryLibraries(ctx)
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("expected 1 library, got %d", len(libs))
	}
	libraryID = libs[0].ID

	// UpsertItem takes Item by value and returns no ID (unlike CreateUser),
	// meaning the caller is the ID authority for items (docs/05: a scanner
	// derives a stable key itself, e.g. from a file path) — so the test
	// mints IDs up front too, exactly as a real caller building a
	// series -> season -> episode tree would.
	seriesID = storage.ItemID(newID())
	series := storage.Item{
		ID:          seriesID,
		Type:        storage.ItemTypeSeries,
		LibraryID:   libraryID,
		Name:        "Example Series",
		SortName:    "example series",
		Genres:      []string{"Drama"},
		DateCreated: time.Now().UTC(),
	}
	if err := st.UpsertItem(ctx, series); err != nil {
		t.Fatalf("UpsertItem(series): %v", err)
	}

	seasonID = storage.ItemID(newID())
	season := storage.Item{
		ID:          seasonID,
		Type:        storage.ItemTypeSeason,
		LibraryID:   libraryID,
		ParentID:    &seriesID,
		SeriesID:    &seriesID,
		Name:        "Season 1",
		SortName:    "season 1",
		DateCreated: time.Now().UTC(),
	}
	if err := st.UpsertItem(ctx, season); err != nil {
		t.Fatalf("UpsertItem(season): %v", err)
	}

	episodeID = storage.ItemID(newID())
	episode := storage.Item{
		ID:          episodeID,
		Type:        storage.ItemTypeEpisode,
		LibraryID:   libraryID,
		ParentID:    &seasonID,
		SeriesID:    &seriesID,
		SeasonID:    &seasonID,
		Name:        "Pilot",
		SortName:    "pilot",
		Overview:    "The one where it all begins.",
		Tags:        []string{"pilot-episode"},
		DateCreated: time.Now().UTC(),
	}
	if err := st.UpsertItem(ctx, episode); err != nil {
		t.Fatalf("UpsertItem(episode): %v", err)
	}

	movieID = storage.ItemID(newID())
	movie := storage.Item{
		ID:          movieID,
		Type:        storage.ItemTypeMovie,
		LibraryID:   libraryID,
		Name:        "Unrelated Movie",
		SortName:    "unrelated movie",
		DateCreated: time.Now().UTC(),
	}
	if err := st.UpsertItem(ctx, movie); err != nil {
		t.Fatalf("UpsertItem(movie): %v", err)
	}

	return libraryID, seriesID, seasonID, episodeID, movieID
}

func TestSeedAndGetItem(t *testing.T) {
	st := openTestStore(t)
	_, seriesID, _, episodeID, _ := seedTree(t, st)

	got, err := st.GetItem(context.Background(), episodeID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Name != "Pilot" {
		t.Errorf("got.Name = %q, want %q", got.Name, "Pilot")
	}
	if got.SeriesID == nil || *got.SeriesID != seriesID {
		t.Errorf("got.SeriesID = %v, want %v", got.SeriesID, seriesID)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "pilot-episode" {
		t.Errorf("got.Tags = %v, want [pilot-episode]", got.Tags)
	}
}

// TestItemPeopleRoundTrips proves storage.Item.People (added alongside
// migration 0002_people.sql) survives an UpsertItem/GetItem round trip via
// the same JSON-column marshal/unmarshal pattern already used for
// Genres/Tags/Taglines.
func TestItemPeopleRoundTrips(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, _, _, _, _ := seedTree(t, st)

	item := storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libraryID, Name: "Cast Test Movie",
		People: []storage.Person{
			{Name: "Keanu Reeves", Role: "Neo", Type: "Actor", TMDBID: 6384},
			{Name: "Lana Wachowski", Type: "Director"},
		},
	}
	if err := st.UpsertItem(ctx, item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	page, err := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &libraryID, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	var itemID storage.ItemID
	for _, it := range page.Items {
		if it.Name == "Cast Test Movie" {
			itemID = it.ID
		}
	}
	if itemID == "" {
		t.Fatal("could not find just-inserted Cast Test Movie")
	}

	got, err := st.GetItem(ctx, itemID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if len(got.People) != 2 {
		t.Fatalf("got.People = %+v, want 2 entries", got.People)
	}
	if got.People[0].Name != "Keanu Reeves" || got.People[0].Role != "Neo" || got.People[0].Type != "Actor" || got.People[0].TMDBID != 6384 {
		t.Errorf("got.People[0] = %+v, want {Keanu Reeves, Neo, Actor, 6384}", got.People[0])
	}
	if got.People[1].Name != "Lana Wachowski" || got.People[1].Type != "Director" {
		t.Errorf("got.People[1] = %+v, want {Lana Wachowski, \"\", Director, 0}", got.People[1])
	}
}

func TestQueryItemsRecursiveVsDirect(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, seriesID, seasonID, episodeID, movieID := seedTree(t, st)

	// Non-recursive under the series: only the season is a direct child.
	direct, err := st.QueryItems(ctx, storage.ItemFilter{ParentID: &seriesID, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (direct): %v", err)
	}
	if direct.TotalCount != 1 || len(direct.Items) != 1 || direct.Items[0].ID != seasonID {
		t.Fatalf("direct children of series = %+v, want exactly [season %q]", direct.Items, seasonID)
	}

	// Recursive under the series: season and episode, not the sibling movie.
	recursive, err := st.QueryItems(ctx, storage.ItemFilter{ParentID: &seriesID, Recursive: true, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (recursive): %v", err)
	}
	if recursive.TotalCount != 2 {
		t.Fatalf("recursive descendants of series: got %d items, want 2 (season + episode); items=%+v", recursive.TotalCount, recursive.Items)
	}
	gotIDs := map[storage.ItemID]bool{}
	for _, it := range recursive.Items {
		gotIDs[it.ID] = true
		if it.ID == movieID {
			t.Errorf("recursive query under series unexpectedly returned the sibling movie %q", movieID)
		}
	}
	if !gotIDs[seasonID] || !gotIDs[episodeID] {
		t.Errorf("recursive descendants missing season or episode: got %+v", gotIDs)
	}

	// Recursive under the season: only the episode (leaf).
	recursiveSeason, err := st.QueryItems(ctx, storage.ItemFilter{ParentID: &seasonID, Recursive: true, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (recursive under season): %v", err)
	}
	if recursiveSeason.TotalCount != 1 || recursiveSeason.Items[0].ID != episodeID {
		t.Fatalf("recursive descendants of season = %+v, want exactly [episode %q]", recursiveSeason.Items, episodeID)
	}

	// Whole-library query returns everything (series, season, episode, movie).
	all, err := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &libraryID, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (library): %v", err)
	}
	if all.TotalCount != 4 {
		t.Fatalf("library query TotalCount = %d, want 4", all.TotalCount)
	}

	// Limit <= 0 must be rejected, not silently return everything.
	if _, err := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &libraryID, Limit: 0}); err == nil {
		t.Error("QueryItems with Limit=0 should return an error")
	}
}

// TestQuerySeriesLatestActivity locks down the GROUP BY series_id/
// MAX(date_created) ranking, and — the subtler regression risk — that the
// aggregate's returned timestamp actually parses back into the right
// instant. A MAX() aggregate column carries no declared type, so
// modernc.org/sqlite hands back the raw stored text instead of
// auto-converting to time.Time the way a plain "date_created" column
// selection does (itemColumns/scanItem); that raw text is in Go's
// time.Time.String() layout (the driver's default write format,
// conn.go's formatTime), not RFC3339 — found by this test failing with a
// Scan type-mismatch error before item.go's parseAggregateTime parsed it
// explicitly with the matching layout. parseAggregateTime's own unit
// tests below cover the monotonic-clock "m=" suffix edge case in
// isolation, independent of host timezone.
func TestQuerySeriesLatestActivity(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, seriesID, _, _, _ := seedTree(t, st)

	// A second series in the same library, with a later episode addition
	// than the first (seedTree's own episode uses time.Now().UTC() at seed
	// time, a moment before this one). .UTC(), matching every real
	// DateCreated write in this project (item.go's UpsertItem default,
	// internal/scan/scan.go, cmd/devseed) — a bare time.Now() would still
	// parse correctly (parseAggregateTime handles it) but would also carry
	// a local UTC offset in its stored text, which sorts inconsistently
	// against UTC-stored rows under a lexicographic SQL ORDER BY whenever
	// the host's local timezone isn't UTC itself; not this test's concern.
	series2ID := storage.ItemID(newID())
	if err := st.UpsertItem(ctx, storage.Item{
		ID: series2ID, Type: storage.ItemTypeSeries, LibraryID: libraryID, Name: "Second Series",
	}); err != nil {
		t.Fatalf("UpsertItem series2: %v", err)
	}
	later := time.Now().UTC().Add(time.Hour)
	if err := st.UpsertItem(ctx, storage.Item{
		ID: storage.ItemID(newID()), Type: storage.ItemTypeEpisode, LibraryID: libraryID,
		SeriesID: &series2ID, Name: "Later Episode", DateCreated: later,
	}); err != nil {
		t.Fatalf("UpsertItem series2 episode: %v", err)
	}

	activity, err := st.QuerySeriesLatestActivity(ctx, libraryID,
		[]storage.ItemType{storage.ItemTypeEpisode, storage.ItemTypeSeason}, 10)
	if err != nil {
		t.Fatalf("QuerySeriesLatestActivity: %v", err)
	}
	if len(activity) != 2 {
		t.Fatalf("QuerySeriesLatestActivity = %+v, want 2 series (the movie sibling seedTree adds must not appear)", activity)
	}
	if activity[0].SeriesID != series2ID {
		t.Errorf("activity[0].SeriesID = %q, want %q (newest-activity-first)", activity[0].SeriesID, series2ID)
	}
	if !activity[0].Latest.Equal(later) {
		t.Errorf("activity[0].Latest = %v, want %v (aggregate timestamp round-tripped correctly)", activity[0].Latest, later)
	}
	if activity[1].SeriesID != seriesID {
		t.Errorf("activity[1].SeriesID = %q, want %q", activity[1].SeriesID, seriesID)
	}

	// Limit is required, same no-full-catalog enforcement as QueryItems.
	if _, err := st.QuerySeriesLatestActivity(ctx, libraryID, nil, 0); err == nil {
		t.Error("QuerySeriesLatestActivity with limit=0 should return an error")
	}
}

// TestParseAggregateTime exercises item.go's parseAggregateTime directly
// against hand-built strings, independent of host timezone or a real
// UpsertItem round-trip — the shapes modernc.org/sqlite's formatTime
// (conn.go) actually produces for a bound time.Time parameter.
func TestParseAggregateTime(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "UTC, no monotonic reading (every real DateCreated this project writes)",
			raw:  "2026-09-02 17:18:03.659681521 +0000 UTC",
			want: time.Date(2026, 9, 2, 17, 18, 3, 659681521, time.UTC),
		},
		{
			name: "non-UTC offset, no monotonic reading",
			raw:  "2026-09-02 22:52:44.400680293 -0300 -03",
			want: time.Date(2026, 9, 2, 22, 52, 44, 400680293, time.FixedZone("", -3*60*60)),
		},
		{
			name: "monotonic-clock \"m=\" suffix stripped (a bare, non-.UTC() time.Now())",
			raw:  "2026-09-02 22:52:28.429374967 -0300 -03 m=+3600.031063515",
			want: time.Date(2026, 9, 2, 22, 52, 28, 429374967, time.FixedZone("", -3*60*60)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAggregateTime(tt.raw)
			if err != nil {
				t.Fatalf("parseAggregateTime(%q): %v", tt.raw, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseAggregateTime(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestQueryItemsSearchText(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, _, _, episodeID, _ := seedTree(t, st)

	res, err := st.QueryItems(ctx, storage.ItemFilter{SearchText: "begins", Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (search): %v", err)
	}
	if res.TotalCount != 1 || res.Items[0].ID != episodeID {
		t.Fatalf("search for %q = %+v, want exactly [episode %q]", "begins", res.Items, episodeID)
	}

	none, err := st.QueryItems(ctx, storage.ItemFilter{SearchText: "nonexistentxyz", Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems (search, no match): %v", err)
	}
	if none.TotalCount != 0 {
		t.Fatalf("search for a nonsense term returned %d results, want 0", none.TotalCount)
	}
}

func TestDeleteItemCascades(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, seriesID, seasonID, episodeID, _ := seedTree(t, st)

	if err := st.DeleteItem(ctx, seriesID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := st.GetItem(ctx, seriesID); err == nil {
		t.Error("expected series to be gone after DeleteItem")
	}
	if _, err := st.GetItem(ctx, seasonID); err == nil {
		t.Error("expected season to cascade-delete with its series")
	}
	if _, err := st.GetItem(ctx, episodeID); err == nil {
		t.Error("expected episode to cascade-delete with its series")
	}
}

func TestTx(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	err := st.Tx(ctx, func(tx storage.Store) error {
		lib := storage.Library{Name: "Movies", CollectionType: storage.CollectionTypeMovies}
		return tx.UpsertLibrary(ctx, lib)
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	libs, err := st.QueryLibraries(ctx)
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 || libs[0].Name != "Movies" {
		t.Fatalf("QueryLibraries after Tx = %+v, want one library named Movies", libs)
	}

	// A failing fn must roll back: nothing from inside it should persist.
	sentinelErr := context.Canceled
	err = st.Tx(ctx, func(tx storage.Store) error {
		if err := tx.UpsertLibrary(ctx, storage.Library{Name: "Should Not Persist"}); err != nil {
			return err
		}
		return sentinelErr
	})
	if err != sentinelErr {
		t.Fatalf("Tx returned %v, want the sentinel error", err)
	}

	libs, err = st.QueryLibraries(ctx)
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("QueryLibraries after rolled-back Tx = %+v, want the rollback to have discarded the second library", libs)
	}
}

func TestUserDeviceAndPlaystate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	userID, err := st.CreateUser(ctx, storage.User{Name: "rafael", IsAdministrator: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if userID == "" {
		t.Fatal("CreateUser returned an empty ID")
	}

	got, err := st.GetUserByName(ctx, "rafael")
	if err != nil {
		t.Fatalf("GetUserByName: %v", err)
	}
	if got.ID != userID {
		t.Errorf("GetUserByName returned ID %q, want %q", got.ID, userID)
	}

	device := storage.Device{Name: "Test Device", AppName: "tinyfin-test"}
	if err := st.UpsertDevice(ctx, device); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	token, err := st.IssueToken(ctx, userID, "")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	looked, err := st.LookupToken(ctx, token.Token)
	if err != nil {
		t.Fatalf("LookupToken: %v", err)
	}
	if looked.UserID != userID {
		t.Errorf("LookupToken UserID = %q, want %q", looked.UserID, userID)
	}

	_, _, _, episodeID, _ := seedTree(t, st)
	if err := st.SetPlaystate(ctx, userID, episodeID, storage.Playstate{Played: true, PlaybackPositionTicks: 12345}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}
	ps, err := st.GetPlaystate(ctx, userID, episodeID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if !ps.Played || ps.PlaybackPositionTicks != 12345 {
		t.Errorf("GetPlaystate = %+v, want Played=true PlaybackPositionTicks=12345", ps)
	}

	if err := st.RevokeToken(ctx, token.Token); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, err := st.LookupToken(ctx, token.Token); err == nil {
		t.Error("expected LookupToken to fail after RevokeToken")
	}
}

// TestConcurrentUpsertItem guards against a real bug internal/scan's
// bounded concurrent scanner found: two goroutines calling UpsertItem for
// a parent item and one of its children at the same time raced
// recomputeAncestors' DELETE+INSERT sequence for the shared child row,
// tripping item_ancestors' UNIQUE constraint (and, separately, plain
// concurrent single-statement writes elsewhere could return "database is
// locked" even within the configured busy_timeout). Store.exec/withTx now
// serialize every top-level write via writeMu (store.go) — this test
// hammers exactly the parent+child pattern that exposed the bug.
func TestConcurrentUpsertItem(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, seriesID, seasonID, _, _ := seedTree(t, st)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			series := storage.Item{
				ID: seriesID, Type: storage.ItemTypeSeries, LibraryID: libraryID,
				Name: "Example Series", SortName: "example series", DateCreated: time.Now().UTC(),
			}
			if err := st.UpsertItem(ctx, series); err != nil {
				errs <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			ep := storage.ItemID(newID())
			episode := storage.Item{
				ID: ep, Type: storage.ItemTypeEpisode, LibraryID: libraryID,
				ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
				Name: fmt.Sprintf("Episode %d", i), SortName: fmt.Sprintf("episode %d", i), DateCreated: time.Now().UTC(),
			}
			if err := st.UpsertItem(ctx, episode); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent UpsertItem: %v", err)
	}
}
