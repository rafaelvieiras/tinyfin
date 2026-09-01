package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// authedClient returns a helper that issues an authenticated request
// (X-Emby-Token) against ts and returns the raw *http.Response.
func authedClient(t *testing.T, ts string, token string) func(method, path string) *http.Response {
	t.Helper()
	return func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, ts+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("X-Emby-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
}

// createTestLibrary inserts a library directly through the storage layer
// and returns its persisted ID — every storage.Item requires a valid
// LibraryID (a NOT NULL foreign key, see the sqlite migration), so any test
// that creates an item needs one of these first.
func createTestLibrary(t *testing.T, st storage.LibraryStore, name string, collectionType storage.CollectionType) storage.LibraryID {
	t.Helper()
	lib := storage.Library{Name: name, CollectionType: collectionType, Paths: []string{"/media/" + name}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	libs, err := st.QueryLibraries(context.Background())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	for _, l := range libs {
		if l.Name == name {
			return l.ID
		}
	}
	t.Fatalf("could not find just-inserted library %q", name)
	return ""
}

// loginToken authenticates user/password against ts and returns the issued
// access token.
func loginToken(t *testing.T, ts, username, password string) string {
	t.Helper()
	resp := authenticate(t, ts, username, password)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticate: status = %d, want 200", resp.StatusCode)
	}
	var authResult authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&authResult); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}
	return authResult.AccessToken
}

func TestUserViewsAndItems(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	item := storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Test Movie",
		Container: "mp4",
		Path:      "/media/movies/test.mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: %v page=%v", err, page)
	}
	itemID := page.Items[0].ID

	// GET /UserViews
	resp := get(http.MethodGet, "/UserViews")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /UserViews status = %d, want 200", resp.StatusCode)
	}
	var views baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decoding /UserViews: %v", err)
	}
	if len(views.Items) != 1 || views.Items[0].Name != "Movies" {
		t.Fatalf("UserViews = %+v, want one item named Movies", views.Items)
	}
	if views.Items[0].Type != "UserView" {
		t.Errorf("UserViews[0].Type = %q, want UserView", views.Items[0].Type)
	}

	// GET /Items
	itemsResp := get(http.MethodGet, "/Items?parentId="+string(libID)+"&recursive=true")
	defer itemsResp.Body.Close()
	if itemsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items status = %d, want 200", itemsResp.StatusCode)
	}
	var items baseItemDtoQueryResult
	if err := json.NewDecoder(itemsResp.Body).Decode(&items); err != nil {
		t.Fatalf("decoding /Items: %v", err)
	}
	if items.TotalRecordCount != 1 || len(items.Items) != 1 {
		t.Fatalf("Items = %+v, want 1 item", items)
	}
	if items.Items[0].Name != "Test Movie" {
		t.Errorf("Items[0].Name = %q, want %q", items.Items[0].Name, "Test Movie")
	}

	// GET /Items/{itemId}
	detailResp := get(http.MethodGet, "/Items/"+string(itemID))
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/{itemId} status = %d, want 200", detailResp.StatusCode)
	}
	var detail baseItemDto
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding item detail: %v", err)
	}
	if detail.Name != "Test Movie" {
		t.Errorf("detail.Name = %q, want %q", detail.Name, "Test Movie")
	}
	if detail.Container != "mp4" {
		t.Errorf("detail.Container = %q, want %q", detail.Container, "mp4")
	}
	if len(detail.MediaStreams) != 2 {
		t.Errorf("len(detail.MediaStreams) = %d, want 2", len(detail.MediaStreams))
	}

	// GET /Items with a default (unspecified) limit still returns results
	// bounded — exercise the defaulting path explicitly.
	defaultLimitResp := get(http.MethodGet, "/Items?parentId="+string(libID)+"&recursive=true")
	defer defaultLimitResp.Body.Close()
	if defaultLimitResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items (default limit) status = %d, want 200", defaultLimitResp.StatusCode)
	}
}

// TestUserItemsResumeFiltersByMediaType regression-guards a real bug found
// via a live jellyfin-web session: the home screen issues three separate
// GET /UserItems/Resume calls, one per mediaTypes value (Video/Audio/Book),
// to build its "Continue Watching"/"Continue Listening"/"Continue Reading"
// rows. handleUserItemsResume ignored mediaTypes entirely, so a resumable
// movie came back — and rendered — in all three rows at once.
func TestUserItemsResumeFiltersByMediaType(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	item := storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Test Movie",
		Container: "mp4",
		Path:      "/media/movies/test.mp4",
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: %v page=%v", err, page)
	}
	itemID := page.Items[0].ID

	if err := st.SetPlaystate(context.Background(), user.ID, itemID, storage.Playstate{PlaybackPositionTicks: 12345}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}

	for _, tc := range []struct {
		mediaType string
		wantCount int
	}{
		{"Video", 1}, // the movie's real MediaType — must come back
		{"Audio", 0}, // this project has no audio items — must NOT come back
		{"Book", 0},  // this project has no book items — must NOT come back
	} {
		resp := get(http.MethodGet, "/UserItems/Resume?mediaTypes="+tc.mediaType)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /UserItems/Resume?mediaTypes=%s status = %d, want 200", tc.mediaType, resp.StatusCode)
		}
		var result baseItemDtoQueryResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decoding /UserItems/Resume?mediaTypes=%s: %v", tc.mediaType, err)
		}
		if len(result.Items) != tc.wantCount {
			t.Errorf("mediaTypes=%s: got %d items, want %d (items=%+v)", tc.mediaType, len(result.Items), tc.wantCount, result.Items)
		}
	}

	// No mediaTypes at all — unfiltered, matches the pre-existing behavior.
	resp := get(http.MethodGet, "/UserItems/Resume")
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding /UserItems/Resume: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("no mediaTypes filter: got %d items, want 1", len(result.Items))
	}
}

// TestItemGetEpisodeIncludesSeriesAndSeasonName regression-guards a real bug
// found via a live jellyfin-web session: an episode's detail page renders
// episode.SeriesName directly in its header (as the literal text
// "undefined" when the field is absent, not a graceful fallback), so a
// BaseItemDto carrying SeriesId/SeasonId without the resolved names still
// visibly breaks the page a real server's response wouldn't.
func TestItemGetEpisodeIncludesSeriesAndSeasonName(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)

	series := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Test Series", Path: "/media/series/Test Series"}
	if err := st.UpsertItem(context.Background(), series); err != nil {
		t.Fatalf("UpsertItem series: %v", err)
	}
	seriesID := mustFindItemByName(t, st, libID, "Test Series")

	season := storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID, Name: "Season 1", Path: "/media/series/Test Series/Season 01"}
	if err := st.UpsertItem(context.Background(), season); err != nil {
		t.Fatalf("UpsertItem season: %v", err)
	}
	seasonID := mustFindItemByName(t, st, libID, "Season 1")

	episode := storage.Item{
		Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
		Name: "Episode 1", Path: "/media/series/Test Series/Season 01/S01E01.mkv",
	}
	if err := st.UpsertItem(context.Background(), episode); err != nil {
		t.Fatalf("UpsertItem episode: %v", err)
	}
	episodeID := mustFindItemByName(t, st, libID, "Episode 1")

	resp := get(http.MethodGet, "/Items/"+string(episodeID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/{itemId} status = %d, want 200", resp.StatusCode)
	}
	var detail baseItemDto
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding episode detail: %v", err)
	}
	if detail.SeriesName != "Test Series" {
		t.Errorf("SeriesName = %q, want %q", detail.SeriesName, "Test Series")
	}
	if detail.SeasonName != "Season 1" {
		t.Errorf("SeasonName = %q, want %q", detail.SeasonName, "Season 1")
	}
}

// mustFindItemByName looks up an item's ID by name within libID — the
// storage layer mints IDs on UpsertItem rather than accepting one, so tests
// that need to chain a parent's ID into a child's ParentID/SeriesID/SeasonID
// have to look it back up.
func mustFindItemByName(t *testing.T, st storage.ItemStore, libID storage.LibraryID, name string) storage.ItemID {
	t.Helper()
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Recursive: true, Limit: 100})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	for _, it := range page.Items {
		if it.Name == name {
			return it.ID
		}
	}
	t.Fatalf("no item named %q found in library %q", name, libID)
	return ""
}

// TestShowSeasonsAndEpisodes regression-guards a real bug found via a live
// jellyfin-web session: GET /Shows/{seriesId}/Seasons and GET
// /Shows/{seriesId}/Episodes didn't exist at all, so a series' detail page
// rendered its header and nothing else — no season list, and (one level
// deeper) no episode list either. A real server needs both: the series page
// calls Seasons, then the season page calls Episodes?seasonId=....
func TestShowSeasonsAndEpisodes(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Séries Infantis", storage.CollectionTypeTVShows)

	series := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Test Series", Path: "/media/series/Test Series"}
	if err := st.UpsertItem(context.Background(), series); err != nil {
		t.Fatalf("UpsertItem series: %v", err)
	}
	seriesID := mustFindItemByName(t, st, libID, "Test Series")

	one := 1
	for _, s := range []struct {
		name string
		idx  int
	}{{"Season 1", 1}, {"Season 2", 2}} {
		idx := s.idx
		season := storage.Item{Type: storage.ItemTypeSeason, LibraryID: libID, ParentID: &seriesID, SeriesID: &seriesID, Name: s.name, IndexNumber: &idx, Path: "/media/series/Test Series/" + s.name}
		if err := st.UpsertItem(context.Background(), season); err != nil {
			t.Fatalf("UpsertItem %s: %v", s.name, err)
		}
	}
	season1ID := mustFindItemByName(t, st, libID, "Season 1")
	season2ID := mustFindItemByName(t, st, libID, "Season 2")

	for _, e := range []struct {
		name     string
		seasonID storage.ItemID
	}{{"S1E1", season1ID}, {"S1E2", season1ID}, {"S2E1", season2ID}} {
		seasonID := e.seasonID
		episode := storage.Item{
			Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seasonID, SeriesID: &seriesID, SeasonID: &seasonID,
			Name: e.name, IndexNumber: &one, Path: "/media/series/Test Series/" + e.name + ".mkv",
		}
		if err := st.UpsertItem(context.Background(), episode); err != nil {
			t.Fatalf("UpsertItem %s: %v", e.name, err)
		}
	}

	// GET /Shows/{seriesId}/Seasons — both seasons, in order.
	seasonsResp := get(http.MethodGet, "/Shows/"+string(seriesID)+"/Seasons")
	defer seasonsResp.Body.Close()
	if seasonsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Shows/{seriesId}/Seasons status = %d, want 200", seasonsResp.StatusCode)
	}
	var seasons baseItemDtoQueryResult
	if err := json.NewDecoder(seasonsResp.Body).Decode(&seasons); err != nil {
		t.Fatalf("decoding Seasons: %v", err)
	}
	if len(seasons.Items) != 2 || seasons.Items[0].Name != "Season 1" || seasons.Items[1].Name != "Season 2" {
		t.Fatalf("Seasons = %+v, want [Season 1, Season 2] in order", seasons.Items)
	}

	// GET /Shows/{seriesId}/Episodes?seasonId=<season1> — only that season's episodes.
	ep1Resp := get(http.MethodGet, "/Shows/"+string(seriesID)+"/Episodes?seasonId="+string(season1ID))
	defer ep1Resp.Body.Close()
	if ep1Resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Shows/{seriesId}/Episodes?seasonId=... status = %d, want 200", ep1Resp.StatusCode)
	}
	var ep1 baseItemDtoQueryResult
	if err := json.NewDecoder(ep1Resp.Body).Decode(&ep1); err != nil {
		t.Fatalf("decoding Episodes (season1): %v", err)
	}
	if len(ep1.Items) != 2 {
		t.Fatalf("Episodes(season1) = %+v, want 2 items", ep1.Items)
	}

	// GET /Shows/{seriesId}/Episodes — no seasonId, every episode of the series.
	allResp := get(http.MethodGet, "/Shows/"+string(seriesID)+"/Episodes")
	defer allResp.Body.Close()
	if allResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Shows/{seriesId}/Episodes status = %d, want 200", allResp.StatusCode)
	}
	var all baseItemDtoQueryResult
	if err := json.NewDecoder(allResp.Body).Decode(&all); err != nil {
		t.Fatalf("decoding Episodes (all): %v", err)
	}
	if len(all.Items) != 3 {
		t.Fatalf("Episodes(all) = %+v, want 3 items", all.Items)
	}
}

func TestItemImageNotFound(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "erin", "erinpass")
	token := loginToken(t, ts.URL, user.Name, "erinpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "NoImageLib", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "No Image Movie"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("QueryItems: %v", err)
	}
	itemID := page.Items[0].ID

	// No scanner has ever populated an image ref for this item — expect a
	// clean 404, not a crash.
	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET .../Images/Primary status = %d, want 404", resp.StatusCode)
	}
}

func TestItemImageServesBytes(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "frank", "frankpass")
	token := loginToken(t, ts.URL, user.Name, "frankpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "ImageLib", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Movie With Image"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("QueryItems: %v", err)
	}
	itemID := page.Items[0].ID

	imgPath := filepath.Join(t.TempDir(), "poster.jpg")
	imgBytes := []byte("fake-jpeg-bytes")
	if err := os.WriteFile(imgPath, imgBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID,
		Kind:   "Primary",
		Index:  0,
		Path:   imgPath,
		Tag:    "abc123",
	}); err != nil {
		t.Fatalf("UpsertImageRef: %v", err)
	}

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../Images/Primary status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading image body: %v", err)
	}
	if string(body) != string(imgBytes) {
		t.Errorf("image body = %q, want %q", body, imgBytes)
	}
}
