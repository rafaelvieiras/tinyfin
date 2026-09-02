package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// seedTestEpisode inserts a series -> season -> episode chain (creating the
// series/season the first time a given name pair is seen, reusing them
// otherwise) and returns the episode's ID. Mirrors the shape
// TestShowSeasonsAndEpisodes (library_test.go) already builds by hand, but
// callable per-episode so nextup_test.go's setup functions read as a plain
// list of episodes rather than a hand-rolled nested loop each time.
func seedTestEpisode(t *testing.T, st storage.ItemStore, libID storage.LibraryID, seriesID, seasonID storage.ItemID, season, episode int, name string) storage.ItemID {
	t.Helper()
	seasonIdx := season
	epIdx := episode
	ep := storage.Item{
		Type:              storage.ItemTypeEpisode,
		LibraryID:         libID,
		ParentID:          &seasonID,
		SeriesID:          &seriesID,
		SeasonID:          &seasonID,
		ParentIndexNumber: &seasonIdx,
		IndexNumber:       &epIdx,
		Name:              name,
		Path:              "/media/" + name,
	}
	if err := st.UpsertItem(context.Background(), ep); err != nil {
		t.Fatalf("UpsertItem(%q): %v", name, err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		SeriesID:  &seriesID,
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode},
		Limit:     1000,
	})
	if err != nil {
		t.Fatalf("QueryItems finding %q: %v", name, err)
	}
	for _, it := range page.Items {
		if it.Name == name {
			return it.ID
		}
	}
	t.Fatalf("could not find just-inserted episode %q", name)
	return ""
}

// seedTestSeries creates a series with one season and returns
// (seriesID, seasonID).
func seedTestSeries(t *testing.T, st storage.ItemStore, libID storage.LibraryID, seriesName, seasonName string) (storage.ItemID, storage.ItemID) {
	t.Helper()
	series := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: seriesName, Path: "/media/" + seriesName}
	if err := st.UpsertItem(context.Background(), series); err != nil {
		t.Fatalf("UpsertItem series %q: %v", seriesName, err)
	}
	seriesID := mustFindItemByName(t, st, libID, seriesName)

	idx := 1
	season := storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID, Name: seasonName, IndexNumber: &idx, Path: "/media/" + seriesName + "/" + seasonName}
	if err := st.UpsertItem(context.Background(), season); err != nil {
		t.Fatalf("UpsertItem season %q: %v", seasonName, err)
	}
	seasonID := mustFindItemByName(t, st, libID, seasonName)
	return seriesID, seasonID
}

// TestNextUpNothingWatched regression-guards the naive-but-wrong reading of
// "next up" as "the first unwatched episode of every series" — confirmed
// against a real Jellyfin server (staging, :8097) that a series with zero
// playback activity produces no NextUp entry at all.
func TestNextUpNothingWatched(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Untouched Series", "Season 1")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 1, "Untouched Episode 1")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 2, "Untouched Episode 2")

	resp := get(http.MethodGet, "/Shows/NextUp")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Items = %+v, want empty (no series has any playback activity)", result.Items)
	}
}

// TestNextUpAdvancesSequentially regression-guards the core rule: after
// watching episode N, NextUp reports episode N+1 — confirmed against a real
// Jellyfin server the same way.
func TestNextUpAdvancesSequentially(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Sequential Series", "Season 1")
	ep1 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 1, "Sequential Episode 1")
	ep2 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 2, "Sequential Episode 2")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 3, "Sequential Episode 3")

	if err := st.SetPlaystate(context.Background(), user.ID, ep1, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
		t.Fatalf("SetPlaystate ep1: %v", err)
	}

	resp := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID))
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != string(ep2) {
		t.Fatalf("Items = %+v, want exactly episode 2 (%s)", result.Items, ep2)
	}
	if result.Items[0].SeriesName != "Sequential Series" {
		t.Errorf("SeriesName = %q, want %q", result.Items[0].SeriesName, "Sequential Series")
	}
}

// TestNextUpSkipsAheadByOrderNotFirstUnwatched regression-guards a
// non-obvious rule confirmed against a real Jellyfin server (staging,
// :8097): marking episode 5 of an otherwise-untouched series played
// advances NextUp straight to episode 6 — it does NOT fall back to episode
// 1 (the first unwatched episode overall), even though 1-4 were never
// watched. A naive "earliest unwatched" implementation of this endpoint
// would fail this test.
func TestNextUpSkipsAheadByOrderNotFirstUnwatched(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Skip Ahead Series", "Season 1")
	for i := 1; i <= 4; i++ {
		seedTestEpisode(t, st, libID, seriesID, seasonID, 1, i, "Skip Episode "+string(rune('0'+i)))
	}
	ep5 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 5, "Skip Episode 5")
	ep6 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 6, "Skip Episode 6")

	if err := st.SetPlaystate(context.Background(), user.ID, ep5, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
		t.Fatalf("SetPlaystate ep5: %v", err)
	}

	resp := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID))
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != string(ep6) {
		t.Fatalf("Items = %+v, want exactly episode 6 (%s), not episode 1", result.Items, ep6)
	}
}

// TestNextUpCrossesSeasonBoundary regression-guards the season-boundary
// case, confirmed against a real Jellyfin server: finishing every episode
// of season 1 advances NextUp to season 2 episode 1, using the same
// ParentIndexNumber-then-IndexNumber order handleShowEpisodes already
// applies for a series' flattened episode list (library.go).
func TestNextUpCrossesSeasonBoundary(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	series := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Multi-Season Series", Path: "/media/multi"}
	if err := st.UpsertItem(context.Background(), series); err != nil {
		t.Fatalf("UpsertItem series: %v", err)
	}
	seriesID := mustFindItemByName(t, st, libID, "Multi-Season Series")

	s1idx, s2idx := 1, 2
	season1 := storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID, Name: "Season One", IndexNumber: &s1idx, Path: "/media/multi/s1"}
	if err := st.UpsertItem(context.Background(), season1); err != nil {
		t.Fatalf("UpsertItem season1: %v", err)
	}
	season1ID := mustFindItemByName(t, st, libID, "Season One")
	season2 := storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID, Name: "Season Two", IndexNumber: &s2idx, Path: "/media/multi/s2"}
	if err := st.UpsertItem(context.Background(), season2); err != nil {
		t.Fatalf("UpsertItem season2: %v", err)
	}
	season2ID := mustFindItemByName(t, st, libID, "Season Two")

	s1e1 := seedTestEpisode(t, st, libID, seriesID, season1ID, 1, 1, "S1E1")
	s1e2 := seedTestEpisode(t, st, libID, seriesID, season1ID, 1, 2, "S1E2")
	s2e1 := seedTestEpisode(t, st, libID, seriesID, season2ID, 2, 1, "S2E1")

	for _, id := range []storage.ItemID{s1e1, s1e2} {
		if err := st.SetPlaystate(context.Background(), user.ID, id, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
			t.Fatalf("SetPlaystate %s: %v", id, err)
		}
	}

	resp := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID))
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != string(s2e1) {
		t.Fatalf("Items = %+v, want exactly season 2 episode 1 (%s)", result.Items, s2e1)
	}
}

// TestNextUpResumableEpisode regression-guards the in-progress case,
// confirmed against a real Jellyfin server: an episode with a saved,
// unfinished resume position (touched — PlayCount > 0 — but not Played)
// surfaces as its own NextUp entry, even with no other episode of the
// series ever watched. enableResumable=false excludes the series entirely
// instead (not falling back to any other candidate) — also confirmed.
func TestNextUpResumableEpisode(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Resumable Series", "Season 1")
	ep1 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 1, "Resumable Episode 1")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 2, "Resumable Episode 2")

	if err := st.SetPlaystate(context.Background(), user.ID, ep1, storage.Playstate{
		Played:                false,
		PlayCount:             1,
		PlaybackPositionTicks: 3_000_000_000,
	}); err != nil {
		t.Fatalf("SetPlaystate ep1: %v", err)
	}

	// Default (enableResumable absent, defaults true): episode 1 itself.
	resp := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID))
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != string(ep1) {
		t.Fatalf("Items = %+v, want exactly the resumable episode 1 (%s)", result.Items, ep1)
	}

	// enableResumable=false: series excluded entirely, not episode 2.
	resp2 := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID)+"&enableResumable=false")
	defer resp2.Body.Close()
	var result2 baseItemDtoQueryResult
	if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result2.Items) != 0 {
		t.Fatalf("Items = %+v, want empty with enableResumable=false", result2.Items)
	}
}

// TestNextUpSeriesIDFilter regression-guards the seriesId query param
// scoping the response to just that series (the series detail page's call
// shape), confirmed against a real Jellyfin server.
func TestNextUpSeriesIDFilter(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)

	seriesAID, seasonAID := seedTestSeries(t, st, libID, "Series A", "Season 1")
	epA1 := seedTestEpisode(t, st, libID, seriesAID, seasonAID, 1, 1, "A Episode 1")
	epA2 := seedTestEpisode(t, st, libID, seriesAID, seasonAID, 1, 2, "A Episode 2")

	seriesBID, seasonBID := seedTestSeries(t, st, libID, "Series B", "Season 1")
	epB1 := seedTestEpisode(t, st, libID, seriesBID, seasonBID, 1, 1, "B Episode 1")
	epB2 := seedTestEpisode(t, st, libID, seriesBID, seasonBID, 1, 2, "B Episode 2")

	if err := st.SetPlaystate(context.Background(), user.ID, epA1, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
		t.Fatalf("SetPlaystate A1: %v", err)
	}
	if err := st.SetPlaystate(context.Background(), user.ID, epB1, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
		t.Fatalf("SetPlaystate B1: %v", err)
	}

	// No seriesId: both series' candidates come back.
	all := get(http.MethodGet, "/Shows/NextUp")
	defer all.Body.Close()
	var allResult baseItemDtoQueryResult
	if err := json.NewDecoder(all.Body).Decode(&allResult); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(allResult.Items) != 2 {
		t.Fatalf("Items = %+v, want 2 (one per series)", allResult.Items)
	}

	// seriesId=A: only A's candidate.
	scoped := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesAID))
	defer scoped.Body.Close()
	var scopedResult baseItemDtoQueryResult
	if err := json.NewDecoder(scoped.Body).Decode(&scopedResult); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(scopedResult.Items) != 1 || scopedResult.Items[0].ID != string(epA2) {
		t.Fatalf("Items = %+v, want exactly A's episode 2 (%s)", scopedResult.Items, epA2)
	}
	_ = epB2
}

// TestNextUpEnableTotalRecordCountFalse regression-guards a genuine
// divergence from GET /Items' own EnableTotalRecordCount=false behavior
// (resolveTotalRecordCount, library.go): confirmed against a real Jellyfin
// server that GET /Shows/NextUp reports TotalRecordCount 0 (not the
// current page's own item count) when this is "false".
func TestNextUpEnableTotalRecordCountFalse(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Count Series", "Season 1")
	ep1 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 1, "Count Episode 1")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 2, "Count Episode 2")
	if err := st.SetPlaystate(context.Background(), user.ID, ep1, storage.Playstate{Played: true, PlayCount: 1}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}

	resp := get(http.MethodGet, "/Shows/NextUp?enableTotalRecordCount=false")
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(result.Items))
	}
	if result.TotalRecordCount != 0 {
		t.Errorf("TotalRecordCount = %d, want 0 (NextUp's EnableTotalRecordCount=false always reports 0, unlike GET /Items)", result.TotalRecordCount)
	}

	resp2 := get(http.MethodGet, "/Shows/NextUp")
	defer resp2.Body.Close()
	var result2 baseItemDtoQueryResult
	if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if result2.TotalRecordCount != 1 {
		t.Errorf("TotalRecordCount (no param) = %d, want 1", result2.TotalRecordCount)
	}
}

// TestNextUpDateCutoffExcludesStaleProgress regression-guards
// nextUpDateCutoff, confirmed against a real Jellyfin server to be a
// *minimum* bound: a series is only included when its last playback
// activity is at or after the cutoff, not before it.
func TestNextUpDateCutoffExcludesStaleProgress(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID, seasonID := seedTestSeries(t, st, libID, "Stale Series", "Season 1")
	ep1 := seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 1, "Stale Episode 1")
	seedTestEpisode(t, st, libID, seriesID, seasonID, 1, 2, "Stale Episode 2")

	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := st.SetPlaystate(context.Background(), user.ID, ep1, storage.Playstate{
		Played: true, PlayCount: 1, LastPlayedDate: &old,
	}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}

	// Cutoff in the future relative to the watch date: excluded. UTC so the
	// formatted value ends in "Z" rather than a "+HH:MM" offset — a literal
	// "+" in a raw query string (not passed through url.Values.Encode) would
	// otherwise be read back as a space by the server's query parser.
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	resp := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID)+"&nextUpDateCutoff="+future)
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Items = %+v, want empty (cutoff after last activity)", result.Items)
	}

	// Cutoff before the watch date: included.
	past := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	resp2 := get(http.MethodGet, "/Shows/NextUp?seriesId="+string(seriesID)+"&nextUpDateCutoff="+past)
	defer resp2.Body.Close()
	var result2 baseItemDtoQueryResult
	if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result2.Items) != 1 {
		t.Errorf("Items = %+v, want 1 (cutoff before last activity)", result2.Items)
	}
}
