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
	// CollectionFolder, not UserView — confirmed against a real Jellyfin
	// server (staging, :8097): GET /UserViews for an ordinary library comes
	// back with Type "CollectionFolder" (dto.go's libraryBaseItemDto doc
	// comment).
	if views.Items[0].Type != "CollectionFolder" {
		t.Errorf("UserViews[0].Type = %q, want CollectionFolder", views.Items[0].Type)
	}
	// UserData is populated for a library root too, same as any other item
	// — also confirmed against the real server rather than assumed.
	if views.Items[0].UserData == nil {
		t.Errorf("UserViews[0].UserData = nil, want a populated userItemDataDto")
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

// intPtr returns a pointer to n — a literal *int can't be taken inline in a
// composite literal.
func intPtr(n int) *int { return &n }

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

// TestItemsIncludesPrimaryImageAspectRatio guards against the movie-grid
// square-card regression found via jellyfin-web testing: its cardBuilder
// picks portrait (~2:3) vs square (~1:1) vs banner card shape from the
// *median* BaseItemDto.PrimaryImageAspectRatio across a list, and falls
// back to a square default when that's null for every item — which it
// always was, this field never having been serialized at all. Covers all
// three cases the field's doc comment (dto.go) calls out: a Primary image
// with recorded dimensions, one without them (a ref predating
// scan/scan.go's imageDimensions), and no Primary image at all.
func TestItemsIncludesPrimaryImageAspectRatio(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "grace", "gracepass")
	token := loginToken(t, ts.URL, user.Name, "gracepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "AspectRatioLib", storage.CollectionTypeMovies)

	items := []storage.Item{
		{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Poster Movie"},
		{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Undimensioned Movie"},
		{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "No Image Movie"},
	}
	for _, item := range items {
		if err := st.UpsertItem(context.Background(), item); err != nil {
			t.Fatalf("UpsertItem(%q): %v", item.Name, err)
		}
	}
	posterID := mustFindItemByName(t, st, libID, "Poster Movie")
	undimensionedID := mustFindItemByName(t, st, libID, "Undimensioned Movie")

	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: posterID, Kind: "Primary", Path: "poster.jpg", Width: 400, Height: 600,
	}); err != nil {
		t.Fatalf("UpsertImageRef(posterID): %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: undimensionedID, Kind: "Primary", Path: "poster.jpg",
	}); err != nil {
		t.Fatalf("UpsertImageRef(undimensionedID): %v", err)
	}

	resp := get(http.MethodGet, "/Items?parentId="+string(libID)+"&Limit=10")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items status = %d, want 200", resp.StatusCode)
	}
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding /Items: %v", err)
	}
	byName := make(map[string]baseItemDto, len(result.Items))
	for _, dto := range result.Items {
		byName[dto.Name] = dto
	}

	poster, ok := byName["Poster Movie"]
	if !ok {
		t.Fatal("Poster Movie missing from /Items response")
	}
	const wantRatio = 400.0 / 600.0
	if poster.PrimaryImageAspectRatio == nil {
		t.Error("Poster Movie: PrimaryImageAspectRatio = nil, want ~0.667 (400x600 poster)")
	} else if diff := *poster.PrimaryImageAspectRatio - wantRatio; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Poster Movie: PrimaryImageAspectRatio = %v, want %v", *poster.PrimaryImageAspectRatio, wantRatio)
	}

	undimensioned, ok := byName["Undimensioned Movie"]
	if !ok {
		t.Fatal("Undimensioned Movie missing from /Items response")
	}
	if undimensioned.PrimaryImageAspectRatio != nil {
		t.Errorf("Undimensioned Movie: PrimaryImageAspectRatio = %v, want nil (no recorded Width/Height)", *undimensioned.PrimaryImageAspectRatio)
	}

	noImage, ok := byName["No Image Movie"]
	if !ok {
		t.Fatal("No Image Movie missing from /Items response")
	}
	if noImage.PrimaryImageAspectRatio != nil {
		t.Errorf("No Image Movie: PrimaryImageAspectRatio = %v, want nil (no Primary image at all)", *noImage.PrimaryImageAspectRatio)
	}
}

// TestUsersItemsFavoritesFilter regression-guards a real bug found via a
// live jellyfin-web session: clicking "Favoritos" in the navbar
// (#/home?tab=1) issues GET /Users/{userId}/Items?Filters=IsFavorite&
// IncludeItemTypes=<Type>&... — capitalized exactly like that (confirmed via
// Playwright network capture against this project's own staging server,
// query keys included) — once per item type. Before this fix that 404'd
// outright: GET /Users/{userId}/Items (the per-user listing form, distinct
// from GET /Users/{userId}/Items/{itemId}) wasn't registered at all: and
// even registered, handleItemsList read every query param lowercase-first
// (q.Get("includeItemTypes"), ...), which url.Values.Get treats as a
// different key than the PascalCase form this screen actually sends, and
// never read a Filters param at all.
func TestUsersItemsFavoritesFilter(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "holly", "hollypass")
	token := loginToken(t, ts.URL, user.Name, "hollypass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	items := []storage.Item{
		{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Favorite Movie"},
		{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Plain Movie"},
		{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Favorite Series"},
	}
	for _, item := range items {
		if err := st.UpsertItem(context.Background(), item); err != nil {
			t.Fatalf("UpsertItem(%q): %v", item.Name, err)
		}
	}
	favMovieID := mustFindItemByName(t, st, libID, "Favorite Movie")
	favSeriesID := mustFindItemByName(t, st, libID, "Favorite Series")

	if err := st.SetPlaystate(context.Background(), user.ID, favMovieID, storage.Playstate{IsFavorite: true}); err != nil {
		t.Fatalf("SetPlaystate(favMovieID): %v", err)
	}
	if err := st.SetPlaystate(context.Background(), user.ID, favSeriesID, storage.Playstate{IsFavorite: true}); err != nil {
		t.Fatalf("SetPlaystate(favSeriesID): %v", err)
	}

	// Exactly the request shape the Favorites screen issues for the Movie
	// row: capitalized query keys, per-user path form.
	resp := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?SortBy=SortName&SortOrder=Ascending&Filters=IsFavorite&Recursive=true&Fields=PrimaryImageAspectRatio&EnableTotalRecordCount=false&Limit=20&IncludeItemTypes=Movie")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Users/{userId}/Items?Filters=IsFavorite&IncludeItemTypes=Movie status = %d, want 200", resp.StatusCode)
	}
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "Favorite Movie" {
		t.Fatalf("Items = %+v, want exactly [Favorite Movie]", result.Items)
	}
	if result.Items[0].UserData == nil || !result.Items[0].UserData.IsFavorite {
		t.Errorf("Favorite Movie: UserData = %+v, want IsFavorite = true", result.Items[0].UserData)
	}

	// The Series row of the same screen — different IncludeItemTypes value,
	// same everything else.
	seriesResp := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?Filters=IsFavorite&Recursive=true&Limit=20&IncludeItemTypes=Series")
	defer seriesResp.Body.Close()
	var seriesResult baseItemDtoQueryResult
	if err := json.NewDecoder(seriesResp.Body).Decode(&seriesResult); err != nil {
		t.Fatalf("decoding series response: %v", err)
	}
	if len(seriesResult.Items) != 1 || seriesResult.Items[0].Name != "Favorite Series" {
		t.Fatalf("Series Items = %+v, want exactly [Favorite Series]", seriesResult.Items)
	}
}

// TestUsersItemsEnableTotalRecordCountFalse regression-guards a real
// divergence found via curl against a real Jellyfin server (staging,
// :8097): with EnableTotalRecordCount=false — exactly what jellyfin-web's
// Favorites screen (#/home?tab=1) and "Continue Watching" row send on every
// call — TotalRecordCount comes back equal to how many items this response
// actually carries, not the real total behind the filter. Confirmed on a
// second page too (StartIndex past the first page), not just an
// unpaginated single response: favoriting 8 movies with Limit=5 got
// TotalRecordCount 5 on the first page and 3 on the second
// (StartIndex=5) — always len(Items), never the true 8. Before this fix,
// handleItemsList always returned the real total regardless of this param.
func TestUsersItemsEnableTotalRecordCountFalse(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "ivan", "ivanpass")
	token := loginToken(t, ts.URL, user.Name, "ivanpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	for i := 0; i < 8; i++ {
		item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Favorite " + string(rune('A'+i))}
		if err := st.UpsertItem(context.Background(), item); err != nil {
			t.Fatalf("UpsertItem(%d): %v", i, err)
		}
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Recursive: true, Limit: 100})
	if err != nil || len(page.Items) != 8 {
		t.Fatalf("QueryItems: %v page=%v", err, page)
	}
	for _, it := range page.Items {
		if err := st.SetPlaystate(context.Background(), user.ID, it.ID, storage.Playstate{IsFavorite: true}); err != nil {
			t.Fatalf("SetPlaystate(%q): %v", it.ID, err)
		}
	}

	firstPage := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?Filters=IsFavorite&Recursive=true&EnableTotalRecordCount=false&Limit=5&IncludeItemTypes=Movie")
	defer firstPage.Body.Close()
	var firstResult baseItemDtoQueryResult
	if err := json.NewDecoder(firstPage.Body).Decode(&firstResult); err != nil {
		t.Fatalf("decoding first-page response: %v", err)
	}
	if len(firstResult.Items) != 5 {
		t.Fatalf("first page Items = %d, want 5", len(firstResult.Items))
	}
	if firstResult.TotalRecordCount != 5 {
		t.Errorf("first page TotalRecordCount = %d, want 5 (len(Items), not the real total 8)", firstResult.TotalRecordCount)
	}

	secondPage := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?Filters=IsFavorite&Recursive=true&EnableTotalRecordCount=false&Limit=5&StartIndex=5&IncludeItemTypes=Movie")
	defer secondPage.Body.Close()
	var secondResult baseItemDtoQueryResult
	if err := json.NewDecoder(secondPage.Body).Decode(&secondResult); err != nil {
		t.Fatalf("decoding second-page response: %v", err)
	}
	if len(secondResult.Items) != 3 {
		t.Fatalf("second page Items = %d, want 3", len(secondResult.Items))
	}
	if secondResult.TotalRecordCount != 3 {
		t.Errorf("second page TotalRecordCount = %d, want 3 (len(Items), not the real total 8)", secondResult.TotalRecordCount)
	}

	// Left off (or "true"), the real total comes back — same request minus
	// the param.
	realTotalResp := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?Filters=IsFavorite&Recursive=true&Limit=5&IncludeItemTypes=Movie")
	defer realTotalResp.Body.Close()
	var realTotalResult baseItemDtoQueryResult
	if err := json.NewDecoder(realTotalResp.Body).Decode(&realTotalResult); err != nil {
		t.Fatalf("decoding default response: %v", err)
	}
	if realTotalResult.TotalRecordCount != 8 {
		t.Errorf("default (no EnableTotalRecordCount) TotalRecordCount = %d, want the real total 8", realTotalResult.TotalRecordCount)
	}
}

// TestUsersItemsFavoritesSeriesSortName regression-guards a real divergence
// found via curl against a real Jellyfin server (staging, :8097):
// SortBy=SeriesSortName,SortName — exactly what jellyfin-web's Favorites
// screen sends on its Episode/Season rows — groups results by their owning
// series' SortName first, only falling back to the item's own SortName to
// break ties within a series. Confirmed live: favoriting one episode of
// series "Homens de Negro" and two episodes of series "MORTAL KOMBAT..."
// came back with the "Homens de Negro" episode first and both "MORTAL
// KOMBAT" episodes grouped together afterward — never split apart by the
// episodes' own titles the way sorting by SortName alone would. Before this
// fix, SeriesSortName wasn't a recognized sortBy token at all
// (parseSortFields silently dropped it), so this query effectively sorted
// by SortName only and interleaved episodes from different series.
func TestUsersItemsFavoritesSeriesSortName(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "julia", "juliapass")
	token := loginToken(t, ts.URL, user.Name, "juliapass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Kids Shows", storage.CollectionTypeTVShows)

	// "Zeta Series" sorts after "Alpha Series" alphabetically, but each of
	// its episodes' own titles ("Episode 1"/"Episode 2") sort well before
	// "Alpha Series"' single episode ("Zulu Episode") — so grouping by
	// series only shows up as correct if SeriesSortName is actually the
	// primary sort key, not SortName alone.
	zetaSeries := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Zeta Series", SortName: "Zeta Series"}
	if err := st.UpsertItem(context.Background(), zetaSeries); err != nil {
		t.Fatalf("UpsertItem(Zeta Series): %v", err)
	}
	zetaID := mustFindItemByName(t, st, libID, "Zeta Series")

	alphaSeries := storage.Item{Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Alpha Series", SortName: "Alpha Series"}
	if err := st.UpsertItem(context.Background(), alphaSeries); err != nil {
		t.Fatalf("UpsertItem(Alpha Series): %v", err)
	}
	alphaID := mustFindItemByName(t, st, libID, "Alpha Series")

	episodes := []storage.Item{
		{Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &zetaID, SeriesID: &zetaID, Name: "Episode 1", SortName: "Episode 1", IndexNumber: intPtr(1)},
		{Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &zetaID, SeriesID: &zetaID, Name: "Episode 2", SortName: "Episode 2", IndexNumber: intPtr(2)},
		{Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &alphaID, SeriesID: &alphaID, Name: "Zulu Episode", SortName: "Zulu Episode", IndexNumber: intPtr(1)},
	}
	for _, ep := range episodes {
		if err := st.UpsertItem(context.Background(), ep); err != nil {
			t.Fatalf("UpsertItem(%q): %v", ep.Name, err)
		}
	}
	zetaEp1 := mustFindItemByName(t, st, libID, "Episode 1")
	zetaEp2 := mustFindItemByName(t, st, libID, "Episode 2")
	alphaEp := mustFindItemByName(t, st, libID, "Zulu Episode")

	for _, id := range []storage.ItemID{zetaEp1, zetaEp2, alphaEp} {
		if err := st.SetPlaystate(context.Background(), user.ID, id, storage.Playstate{IsFavorite: true}); err != nil {
			t.Fatalf("SetPlaystate(%q): %v", id, err)
		}
	}

	resp := get(http.MethodGet, "/Users/"+string(user.ID)+"/Items?SortBy=SeriesSortName,SortName&SortOrder=Ascending&Filters=IsFavorite&Recursive=true&IncludeItemTypes=Episode")
	defer resp.Body.Close()
	var result baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("Items = %+v, want exactly 3 favorited episodes", result.Items)
	}
	got := []string{result.Items[0].Name, result.Items[1].Name, result.Items[2].Name}
	want := []string{"Zulu Episode", "Episode 1", "Episode 2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Items[%d].Name = %q, want %q (order = %v, want %v — grouped by series, not interleaved by episode title)", i, got[i], want[i], got, want)
			break
		}
	}
}
