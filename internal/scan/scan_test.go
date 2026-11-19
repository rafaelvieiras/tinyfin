package scan

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// writeFile creates path (and its parent dirs) with dummy content — real
// ffprobe output isn't required for this test: Scan tolerates a probe
// failure (probe.go's doc comment) and still catalogs the item, which is
// exactly the behavior this test relies on to stay independent of whether
// ffprobe is installed in the environment running it.
func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// fakeProvider is a metadata.Provider double that records every Query it
// receives and returns a canned Result keyed by Kind, letting scan_test.go
// verify both what Scan asked for and that the Result got applied to the
// stored Item.
type fakeProvider struct {
	imageURL string

	mu      sync.Mutex
	queries []metadata.Query
}

func (f *fakeProvider) Lookup(ctx context.Context, q metadata.Query) (metadata.Result, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	switch q.Kind {
	case metadata.KindMovie:
		return metadata.Result{
			Overview:    "A hacker discovers reality is a simulation.",
			Genres:      []string{"Action", "Science Fiction"},
			Images:      []metadata.Image{{Kind: "Primary", URL: f.imageURL}},
			ExternalIDs: map[string]string{"Tmdb": "603"},
		}, nil
	case metadata.KindSeries:
		return metadata.Result{
			Overview:    "A long-running anime.",
			Studios:     []string{"Fuji TV"},
			EndDate:     1234567890,
			ExternalIDs: map[string]string{"Tmdb": "12"},
		}, nil
	case metadata.KindSeason:
		return metadata.Result{Title: "Saiyan Saga", Studios: []string{"Fuji TV"}}, nil
	case metadata.KindEpisode:
		return metadata.Result{Overview: "An episode overview."}, nil
	}
	return metadata.Result{}, metadata.ErrNotFound
}

func TestScanLibraryMovies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "not a real video, just enough to exist")

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testPosterBytes(t))
	}))
	defer imgSrv.Close()

	st := newTestStore(t)
	provider := &fakeProvider{imageURL: imgSrv.URL + "/poster.jpg"}
	imageDir := t.TempDir()
	s := New(st, provider, Config{ImageDir: imageDir, ImageHosts: imageHostsFor(imgSrv)})

	lib := storage.Library{
		ID: "lib-movies", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	stats, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if stats.ItemsScanned != 1 {
		t.Fatalf("ItemsScanned = %d, want 1", stats.ItemsScanned)
	}
	if stats.ItemsEnriched != 1 {
		t.Fatalf("ItemsEnriched = %d, want 1", stats.ItemsEnriched)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(page.Items))
	}
	item := page.Items[0]
	if item.Name != "The Matrix (1999)" {
		t.Errorf("Name = %q, want %q", item.Name, "The Matrix (1999)")
	}
	if item.ProductionYear == nil || *item.ProductionYear != 1999 {
		t.Errorf("ProductionYear = %v, want 1999", item.ProductionYear)
	}
	if item.Overview == "" {
		t.Error("Overview is empty, want the provider's enrichment applied")
	}
	if item.ProviderIDs["Tmdb"] != "603" {
		t.Errorf("ProviderIDs[Tmdb] = %q, want 603", item.ProviderIDs["Tmdb"])
	}

	refs, err := st.GetImageRefs(context.Background(), item.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(image refs) = %d, want 1 (downloaded provider image)", len(refs))
	}
	if _, err := os.Stat(refs[0].Path); err != nil {
		t.Errorf("downloaded image file missing at %s: %v", refs[0].Path, err)
	}

	// Re-scanning the same tree must upsert the same item, not duplicate it.
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("second ScanLibrary: %v", err)
	}
	page2, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryItems after re-scan: %v", err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("len(Items) after re-scan = %d, want 1 (re-scan must not duplicate)", len(page2.Items))
	}
}

// TestScanLibraryMovieDateCreatedFollowsFileModTime proves a freshly
// discovered movie's DateCreated comes from its video file's own mtime, not
// the wall-clock moment the scan happened to reach it — otherwise a bulk
// import of an existing collection (every file already on disk before the
// first scan ever runs) would sort "recently added" by directory-traversal
// order instead of by when each file actually landed on disk, which is what
// the reference server's own default does (it seeds DateCreated from the
// file's own filesystem timestamp, confirmed by reading jellyfin/jellyfin's
// resolver behavior). Two movies are written newest-name-first but with
// mtimes set to the opposite order, so a pass that only reflects scan order
// would get this backwards.
func TestScanLibraryMovieDateCreatedFollowsFileModTime(t *testing.T) {
	root := t.TempDir()
	olderPath := filepath.Join(root, "A Older Movie (2000)", "movie.mkv")
	newerPath := filepath.Join(root, "Z Newer Movie (2020)", "movie.mkv")
	writeFile(t, olderPath, "older")
	writeFile(t, newerPath, "newer")

	older := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(olderPath, older, older); err != nil {
		t.Fatalf("Chtimes older: %v", err)
	}
	if err := os.Chtimes(newerPath, newer, newer); err != nil {
		t.Fatalf("Chtimes newer: %v", err)
	}

	st := newTestStore(t)
	s := New(st, &fakeProvider{}, Config{})
	lib := storage.Library{
		ID: "lib-movies-mtime", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie},
		SortBy: []storage.SortField{{By: storage.SortByDateCreated, Descending: true}}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(page.Items))
	}
	if page.Items[0].Name != "Z Newer Movie (2020)" || page.Items[1].Name != "A Older Movie (2000)" {
		t.Errorf("DateCreated-descending order = [%q, %q], want [\"Z Newer Movie\", \"A Older Movie\"] (file mtime order, not alphabetical scan order)",
			page.Items[0].Name, page.Items[1].Name)
	}
}

func TestScanLibraryTVShows(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "Example Show (1986)", "Season 01")
	writeFile(t, filepath.Join(base, "Example Show - S01E01 - The Secret of the Example Orbs.mkv"), "ep1")
	writeFile(t, filepath.Join(base, "Example Show - S01E02 - Second Episode.mkv"), "ep2")
	// A non-matching file in the season folder should be skipped, not crash the scan.
	writeFile(t, filepath.Join(base, "notes.txt"), "irrelevant")

	st := newTestStore(t)
	provider := &fakeProvider{}
	s := New(st, provider, Config{})

	lib := storage.Library{
		ID: "lib-tv", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	stats, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	// 1 series + 1 season + 2 episodes go through the bounded job pool as
	// enrich jobs (the season/series *upserts* themselves happen
	// synchronously during the walk, not as pool jobs — see scan.go; only
	// their enrich-and-reupsert step is enqueued).
	if stats.ItemsScanned != 4 {
		t.Fatalf("ItemsScanned = %d, want 4 (1 series + 1 season + 2 episodes); errors: %v", stats.ItemsScanned, stats.Errors)
	}

	seriesPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeries}, Limit: 10,
	})
	if err != nil || len(seriesPage.Items) != 1 {
		t.Fatalf("series query: err=%v, len=%d, want 1", err, len(seriesPage.Items))
	}
	series := seriesPage.Items[0]
	if series.Name != "Example Show" {
		t.Errorf("series.Name = %q, want %q", series.Name, "Example Show")
	}
	if series.Overview == "" {
		t.Error("series.Overview is empty, want provider enrichment applied")
	}
	if len(series.Studios) != 1 || series.Studios[0] != "Fuji TV" {
		t.Errorf("series.Studios = %v, want [Fuji TV] (enrich() overlays Result.Studios)", series.Studios)
	}
	if series.EndDate == nil {
		t.Error("series.EndDate is nil, want enrich() to overlay Result.EndDate")
	}
	// Series and Season carry their own folder as Path (the reference server
	// reports the directory there; the conformance fixtures match on it).
	if want := filepath.Join(root, "Example Show (1986)"); series.Path != want {
		t.Errorf("series.Path = %q, want %q", series.Path, want)
	}

	seasonPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ParentID: &series.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeason}, Limit: 10,
	})
	if err != nil || len(seasonPage.Items) != 1 {
		t.Fatalf("season query: err=%v, len=%d, want 1", err, len(seasonPage.Items))
	}
	season := seasonPage.Items[0]
	if season.IndexNumber == nil || *season.IndexNumber != 1 {
		t.Errorf("season.IndexNumber = %v, want 1", season.IndexNumber)
	}
	if season.Path != base {
		t.Errorf("season.Path = %q, want %q", season.Path, base)
	}
	if season.Name != "Saiyan Saga" {
		t.Errorf("season.Name = %q, want %q (provider enrichment applied, not the generic \"Season 1\" fallback)", season.Name, "Saiyan Saga")
	}
	if len(season.Studios) != 1 || season.Studios[0] != "Fuji TV" {
		t.Errorf("season.Studios = %v, want [Fuji TV]", season.Studios)
	}

	epPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ParentID: &season.ID, ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}, Limit: 10,
		SortBy: []storage.SortField{{By: storage.SortByIndexNumber}},
	})
	if err != nil || len(epPage.Items) != 2 {
		t.Fatalf("episode query: err=%v, len=%d, want 2", err, len(epPage.Items))
	}
	ep1 := epPage.Items[0]
	if ep1.Name != "Example Show - S01E01 - The Secret of the Example Orbs" {
		t.Errorf("ep1.Name = %q, want the file name without its extension", ep1.Name)
	}
	if ep1.IndexNumber == nil || *ep1.IndexNumber != 1 {
		t.Errorf("ep1.IndexNumber = %v, want 1", ep1.IndexNumber)
	}
	if ep1.ParentIndexNumber == nil || *ep1.ParentIndexNumber != 1 {
		t.Errorf("ep1.ParentIndexNumber = %v, want 1", ep1.ParentIndexNumber)
	}
	if ep1.SeriesID == nil || *ep1.SeriesID != series.ID {
		t.Errorf("ep1.SeriesID = %v, want %v", ep1.SeriesID, series.ID)
	}
	if ep1.SeasonID == nil || *ep1.SeasonID != season.ID {
		t.Errorf("ep1.SeasonID = %v, want %v", ep1.SeasonID, season.ID)
	}
}

func TestScanLibraryNFOOverridesProvider(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(movieDir, "movie.mkv"), "not a real video, just enough to exist")
	writeFile(t, filepath.Join(movieDir, "movie.nfo"), `<movie>
  <title>The Matrix</title>
  <plot>Overview from the local .nfo, must win over the provider.</plot>
  <genre>Cyberpunk</genre>
  <uniqueid type="tmdb">999</uniqueid>
</movie>`)

	st := newTestStore(t)
	provider := &fakeProvider{} // fakeProvider's KindMovie result has a different Overview/Genres/Tmdb ID
	s := New(st, provider, Config{})

	lib := storage.Library{
		ID: "lib-nfo", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	wantOverview := "Overview from the local .nfo, must win over the provider."
	if item.Overview != wantOverview {
		t.Errorf("Overview = %q, want %q (local .nfo must win over provider)", item.Overview, wantOverview)
	}
	if len(item.Genres) != 1 || item.Genres[0] != "Cyberpunk" {
		t.Errorf("Genres = %v, want [Cyberpunk] (local .nfo must win over provider's [Action Science Fiction])", item.Genres)
	}
	if item.ProviderIDs["Tmdb"] != "999" {
		t.Errorf("ProviderIDs[Tmdb] = %q, want %q (local .nfo must win over provider's 603)", item.ProviderIDs["Tmdb"], "999")
	}
}

// TestScanLibraryFolderJPGDetected proves seedFolderImages recognizes
// folder.jpg (a valid Kodi/Jellyfin Primary-image filename), not just
// poster.jpg.
func TestScanLibraryFolderJPGDetected(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "Some Movie (2020)")
	writeFile(t, filepath.Join(movieDir, "movie.mkv"), "content")
	writeFile(t, filepath.Join(movieDir, "folder.jpg"), "fake-jpeg-bytes")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{ID: "lib-folderjpg", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	refs, err := st.GetImageRefs(context.Background(), item.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(Primary image refs) = %d, want 1 (folder.jpg should be detected)", len(refs))
	}
	if filepath.Base(refs[0].Path) != "folder.jpg" {
		t.Errorf("image ref path = %q, want basename %q", refs[0].Path, "folder.jpg")
	}
}

// peopleProvider is a metadata.Provider double that always returns a fixed
// People list on a Movie lookup — used to prove enrich/peopleFromResult
// actually applies Result.People onto the cataloged storage.Item, distinct
// from fakeProvider above (which doesn't set People at all, so every other
// scan_test.go test using it stays unaffected by this feature).
type peopleProvider struct{}

func (peopleProvider) Lookup(ctx context.Context, q metadata.Query) (metadata.Result, error) {
	if q.Kind != metadata.KindMovie {
		return metadata.Result{}, metadata.ErrNotFound
	}
	return metadata.Result{
		Overview: "overview",
		People: []metadata.Person{
			{Name: "Keanu Reeves", Role: "Neo", Type: "Actor", TMDBID: 6384},
			{Name: "Lana Wachowski", Type: "Director"},
		},
	}, nil
}

// TestScanLibraryMovieAppliesPeopleFromProvider proves a Provider's
// Result.People (TMDBProvider's new credits mapping, tmdb.go's
// creditsToPeople) makes it onto the cataloged storage.Item.People —
// the internal/metadata -> internal/scan -> internal/storage conversion
// chain this feature's cast/crew support depends on end to end.
func TestScanLibraryMovieAppliesPeopleFromProvider(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "content")

	st := newTestStore(t)
	s := New(st, peopleProvider{}, Config{})

	lib := storage.Library{ID: "lib-people", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]
	if len(item.People) != 2 {
		t.Fatalf("len(People) = %d, want 2; got %+v", len(item.People), item.People)
	}
	if item.People[0].Name != "Keanu Reeves" || item.People[0].Role != "Neo" || item.People[0].Type != "Actor" || item.People[0].TMDBID != 6384 {
		t.Errorf("People[0] = %+v, want {Keanu Reeves, Neo, Actor, 6384}", item.People[0])
	}
	if item.People[1].Name != "Lana Wachowski" || item.People[1].Type != "Director" {
		t.Errorf("People[1] = %+v, want {Lana Wachowski, \"\", Director, 0}", item.People[1])
	}
}

// TestScanLibraryLogoAndBannerDetected regression-guards docs/12's "Movie
// detail page — local Logo/clearlogo artwork recognition": a movie folder
// with a logo.png (or banner.png) sitting next to poster.jpg/fanart.jpg
// used to be scanned for Primary/Backdrop only, leaving
// GET /Items/{id}/Images/Logo 404ing even with the file present on disk.
func TestScanLibraryLogoAndBannerDetected(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "Some Movie (2020)")
	writeFile(t, filepath.Join(movieDir, "movie.mkv"), "content")
	writeFile(t, filepath.Join(movieDir, "logo.png"), "fake-png-bytes")
	writeFile(t, filepath.Join(movieDir, "banner.jpg"), "fake-jpeg-bytes")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{ID: "lib-logobanner", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	logoRefs, err := st.GetImageRefs(context.Background(), item.ID, "Logo")
	if err != nil {
		t.Fatalf("GetImageRefs(Logo): %v", err)
	}
	if len(logoRefs) != 1 {
		t.Fatalf("len(Logo image refs) = %d, want 1 (logo.png should be detected)", len(logoRefs))
	}
	if filepath.Base(logoRefs[0].Path) != "logo.png" {
		t.Errorf("Logo image ref path = %q, want basename %q", logoRefs[0].Path, "logo.png")
	}

	bannerRefs, err := st.GetImageRefs(context.Background(), item.ID, "Banner")
	if err != nil {
		t.Fatalf("GetImageRefs(Banner): %v", err)
	}
	if len(bannerRefs) != 1 {
		t.Fatalf("len(Banner image refs) = %d, want 1 (banner.jpg should be detected)", len(bannerRefs))
	}
	if filepath.Base(bannerRefs[0].Path) != "banner.jpg" {
		t.Errorf("Banner image ref path = %q, want basename %q", bannerRefs[0].Path, "banner.jpg")
	}
}

// TestScanLibrarySeasonPosterDetected regression-guards the Kodi
// season-poster convention (kodi.wiki's "Artwork" guide, "Season" section):
// a season{NN}-poster.jpg sitting in the *series root* folder (not inside
// the "Season NN" subfolder) must be recorded as that season item's Primary
// image, the same way poster.jpg/folder.jpg already are for a movie/series
// folder itself. Reproduces the real-world gap found comparing tinyfin's
// Example Show Season 1 card (no ImageTags.Primary) against the reference
// server for a library that has a real season01-poster.jpg on disk.
func TestScanLibrarySeasonPosterDetected(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Example Show (1986)")
	base := filepath.Join(seriesDir, "Season 01")
	writeFile(t, filepath.Join(base, "Example Show - S01E01 - The Secret of the Example Orbs.mkv"), "ep1")
	writeFile(t, filepath.Join(seriesDir, "poster.jpg"), "series-poster-bytes")
	writeFile(t, filepath.Join(seriesDir, "season01-poster.jpg"), "season-poster-bytes")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{ID: "lib-seasonposter", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	seasonPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeason}, Limit: 10,
	})
	if err != nil || len(seasonPage.Items) != 1 {
		t.Fatalf("season query: err=%v, len=%d, want 1", err, len(seasonPage.Items))
	}
	season := seasonPage.Items[0]

	refs, err := st.GetImageRefs(context.Background(), season.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs(Primary): %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(season Primary image refs) = %d, want 1 (season01-poster.jpg should be detected)", len(refs))
	}
	if filepath.Base(refs[0].Path) != "season01-poster.jpg" {
		t.Errorf("season image ref path = %q, want basename %q", refs[0].Path, "season01-poster.jpg")
	}
}

func TestScanLibraryNoProviderStillCatalogs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Some Movie (2020)", "movie.mkv"), "content")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{ID: "lib-1", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	st.UpsertLibrary(context.Background(), lib)

	stats, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if stats.ItemsScanned != 1 {
		t.Fatalf("ItemsScanned = %d, want 1", stats.ItemsScanned)
	}
	if stats.ItemsEnriched != 0 {
		t.Fatalf("ItemsEnriched = %d, want 0 (no provider configured)", stats.ItemsEnriched)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Some Movie (2020)" {
		t.Fatalf("Items = %+v, want one item named %q", page.Items, "Some Movie (2020)")
	}
}

// TestScanLibraryRecordsPosterDimensions guards against the movie-grid
// square-card regression: jellyfin-web's cardBuilder picks portrait vs
// square vs banner from BaseItemDto.PrimaryImageAspectRatio (api/dto.go),
// which the API derives from ImageRef.Width/Height. A provider image that
// scan stores without those dimensions makes every card fall back to
// jellyfin-web's square default regardless of the poster's real shape —
// this test catches that regression at the scanner (population), leaving
// api/dto_test.go's TestBaseItemDtoPrimaryImageAspectRatio to catch it at
// the API (consumption).
func TestScanLibraryRecordsPosterDimensions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "not a real video, just enough to exist")

	// A real 400x600 (2:3 poster) PNG: image.DecodeConfig needs an
	// actually-decodable image to report dimensions from.
	posterBytes := testPosterBytes(t)

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(posterBytes)
	}))
	defer imgSrv.Close()

	st := newTestStore(t)
	provider := &fakeProvider{imageURL: imgSrv.URL + "/poster.jpg"}
	s := New(st, provider, Config{ImageDir: t.TempDir(), ImageHosts: imageHostsFor(imgSrv)})

	lib := storage.Library{
		ID: "lib-movies", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: %v page=%v", err, page)
	}

	refs, err := st.GetImageRefs(context.Background(), page.Items[0].ID, "Primary")
	if err != nil || len(refs) != 1 {
		t.Fatalf("GetImageRefs: %v refs=%v", err, refs)
	}
	if refs[0].Width != 400 || refs[0].Height != 600 {
		t.Errorf("ImageRef.Width/Height = %d/%d, want 400/600", refs[0].Width, refs[0].Height)
	}
}

// TestScanLibraryReconcilesLegacyItemIDByPath reproduces the duplication
// bug found via POST /Library/Refresh on a long-lived test deployment: cmd/devseed (a throwaway
// manual-seeding tool, not part of the scanner) minted item IDs with its
// own "item-"+slug(folder) scheme, distinct from the real scanner's
// "item-movie-"+path. Rescanning a devseed-seeded library used to catalog
// every file a second time under the scanner's own ID, silently doubling
// the library instead of updating the existing row. reconcileItemID
// (scan.go) closes this by looking the path up in the store before
// minting a fresh ID.
func TestScanLibraryReconcilesLegacyItemIDByPath(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "The Matrix (1999)")
	videoPath := filepath.Join(movieDir, "movie.mkv")
	writeFile(t, videoPath, "not a real video, just enough to exist")

	st := newTestStore(t)
	lib := storage.Library{
		ID: "lib-legacy", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	// Simulate a pre-existing row at this exact path, cataloged under an ID
	// the current scanner would never mint itself (devseed's legacy scheme,
	// or any future re-derivation) — this is the row a rescan must update
	// in place, not duplicate.
	legacyID := storage.ItemID("legacy-devseed-id-0001")
	legacyItem := storage.Item{
		ID: legacyID, Type: storage.ItemTypeMovie, LibraryID: lib.ID,
		Name: "The Matrix (stale name)", SortName: "The Matrix (stale name)",
		Path: videoPath, DateCreated: time.Now(),
	}
	if err := st.UpsertItem(context.Background(), legacyItem); err != nil {
		t.Fatalf("UpsertItem (legacy): %v", err)
	}

	s := New(st, metadata.NoopProvider{}, Config{})
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1 (rescan must not duplicate the legacy row); items=%+v", len(page.Items), page.Items)
	}
	item := page.Items[0]
	if item.ID != legacyID {
		t.Errorf("Item.ID = %q, want %q (rescan must reuse the pre-existing row's ID, not mint a new one)", item.ID, legacyID)
	}
	if item.Name != "The Matrix (1999)" {
		t.Errorf("Item.Name = %q, want %q (rescan must still refresh the reused row's fields)", item.Name, "The Matrix (1999)")
	}
}

// TestScanLibraryOverlappingPathsDoNotCollide reproduces a real
// incident: two libraries (e.g. a leftover manually-created test library)
// pointing at the same host folder. Before the fix, deterministicID hashed
// only the file's path, and reconcileItemID's existing-row lookup was
// scoped to the library currently being scanned — so scanning libB over a
// path libA had already cataloged minted the *same* item ID libA was
// already using, and UpsertItem's ON CONFLICT(id) silently reassigned that
// row's LibraryID from libA to libB. Concurrent "Scan All Libraries" runs
// (Server.refreshAllLibraries) turned this into a race that split one
// library's catalog into two nondeterministically — the exact symptom
// a reporter reported live against a test deployment. Scoping the hash by
// LibraryID (scan.go's four deterministicID call sites) means the two
// libraries now mint distinct IDs for the same physical file instead of
// fighting over one row.
func TestScanLibraryOverlappingPathsDoNotCollide(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "not a real video, just enough to exist")

	st := newTestStore(t)
	libA := storage.Library{
		ID: "lib-a", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	libB := storage.Library{
		ID: "lib-b", Name: "RenamedLibrary", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), libA); err != nil {
		t.Fatalf("UpsertLibrary(libA): %v", err)
	}
	if err := st.UpsertLibrary(context.Background(), libB); err != nil {
		t.Fatalf("UpsertLibrary(libB): %v", err)
	}

	s := New(st, metadata.NoopProvider{}, Config{})
	if _, err := s.ScanLibrary(context.Background(), libA); err != nil {
		t.Fatalf("ScanLibrary(libA): %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), libB); err != nil {
		t.Fatalf("ScanLibrary(libB): %v", err)
	}

	pageA, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libA.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems(libA): %v", err)
	}
	if len(pageA.Items) != 1 {
		t.Fatalf("len(libA.Items) = %d, want 1 (scanning libB over the same path must not steal libA's item)", len(pageA.Items))
	}

	pageB, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libB.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems(libB): %v", err)
	}
	if len(pageB.Items) != 1 {
		t.Fatalf("len(libB.Items) = %d, want 1 (libB should catalog the same file as its own item)", len(pageB.Items))
	}

	if pageA.Items[0].ID == pageB.Items[0].ID {
		t.Errorf("libA and libB item IDs collide (%q) — should be distinct per library", pageA.Items[0].ID)
	}
}

// TestRefreshItemMovie covers RefreshItem's Movie path: a already-scanned
// movie with a field cleared (simulating an incomplete/stale item) gets it
// back after RefreshItem, and the re-fetch reuses the item's own
// already-known Tmdb ID (Query.ExternalIDs) rather than re-searching by
// title from scratch.
func TestRefreshItemMovie(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "not a real video, just enough to exist")

	st := newTestStore(t)
	provider := &fakeProvider{}
	s := New(st, provider, Config{})

	lib := storage.Library{
		ID: "lib-movies", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	item.Overview = ""
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem (clearing Overview): %v", err)
	}

	if err := s.RefreshItem(context.Background(), item.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	after, err := st.GetItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetItem after RefreshItem: %v", err)
	}
	if after.Overview == "" {
		t.Error("Overview still empty after RefreshItem, want provider re-enrichment applied")
	}

	last := provider.queries[len(provider.queries)-1]
	if last.ExternalIDs["Tmdb"] != "603" {
		t.Errorf("RefreshItem query ExternalIDs[Tmdb] = %q, want %q (item's own already-known ProviderIDs)", last.ExternalIDs["Tmdb"], "603")
	}
}

// TestRefreshItemEpisodeUsesSeriesExternalID covers RefreshItem's
// Season/Episode path: the query it sends must carry the *parent series'*
// ProviderIDs as ExternalIDs (what TMDBProvider.resolveSeriesID expects,
// tmdb.go), not the episode's own — an episode has no independent Tmdb ID
// in this project's Result shape, only the series does.
func TestRefreshItemEpisodeUsesSeriesExternalID(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "Example Show (1986)", "Season 01")
	writeFile(t, filepath.Join(base, "Example Show - S01E01 - The Secret of the Example Orbs.mkv"), "ep1")

	st := newTestStore(t)
	provider := &fakeProvider{}
	s := New(st, provider, Config{})

	lib := storage.Library{
		ID: "lib-tv", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	epPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}, Limit: 10,
	})
	if err != nil || len(epPage.Items) != 1 {
		t.Fatalf("episode query: err=%v, len=%d, want 1", err, len(epPage.Items))
	}
	ep := epPage.Items[0]

	if err := s.RefreshItem(context.Background(), ep.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	last := provider.queries[len(provider.queries)-1]
	if last.Kind != metadata.KindEpisode {
		t.Fatalf("last query Kind = %q, want %q", last.Kind, metadata.KindEpisode)
	}
	if last.SeriesTitle != "Example Show" {
		t.Errorf("last query SeriesTitle = %q, want %q", last.SeriesTitle, "Example Show")
	}
	if last.ExternalIDs["Tmdb"] != "12" {
		t.Errorf("last query ExternalIDs[Tmdb] = %q, want %q (parent series' Tmdb ID, from the earlier scan's enrichment)", last.ExternalIDs["Tmdb"], "12")
	}
}

// TestRefreshItemUnsupportedTypeNoOp covers an ItemType this scanner has no
// enrichment path for at all (same set ScanLibrary itself never enriches,
// docs/08's video-first v1 scope) — RefreshItem must not call the provider
// or error, just do nothing.
func TestRefreshItemUnsupportedTypeNoOp(t *testing.T) {
	st := newTestStore(t)
	provider := &fakeProvider{}
	s := New(st, provider, Config{})

	lib := storage.Library{ID: "lib-music", Name: "Music", CollectionType: storage.CollectionTypeMusic}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if err := st.UpsertItem(context.Background(), storage.Item{
		ID: "item-song", Type: storage.ItemTypeAudio, LibraryID: lib.ID, Name: "Some Song",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	if err := s.RefreshItem(context.Background(), "item-song", false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	if len(provider.queries) != 0 {
		t.Errorf("provider.queries = %v, want none — an Audio item should never reach the provider", provider.queries)
	}
}

// TestRefreshItemSeriesAppliesLocalNFOAndArtwork is the exact regression
// case a real bug report described: a series scanned with no metadata
// provider configured (NoopProvider) stays incomplete, and its media
// folder's own tvshow.nfo/poster.jpg — which ScanLibrary already knows how
// to read — never gets picked up by a later "Refresh metadata" click,
// because the old RefreshItem only ever asked the remote Provider (which
// still has nothing) and never re-read anything local. Series stores no
// Path of its own (RefreshItem's doc comment), so this also exercises
// seriesFolder's derive-from-a-child-episode path.
func TestRefreshItemSeriesAppliesLocalNFOAndArtwork(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Omega Series")
	base := filepath.Join(seriesDir, "Season 01")
	writeFile(t, filepath.Join(base, "Omega Series - S01E01 - Pilot.mkv"), "ep1")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{
		ID: "lib-mk", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	seriesPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeries}, Limit: 10,
	})
	if err != nil || len(seriesPage.Items) != 1 {
		t.Fatalf("series query: err=%v, len=%d, want 1", err, len(seriesPage.Items))
	}
	series := seriesPage.Items[0]
	if series.Overview != "" {
		t.Fatalf("series.Overview = %q before adding a local .nfo, want empty (sanity check)", series.Overview)
	}

	// The media folder gaining a tvshow.nfo/poster.jpg after the initial
	// scan (or simply never having been read before) is exactly the bug
	// report's scenario.
	writeFile(t, filepath.Join(seriesDir, "tvshow.nfo"), `<tvshow>
  <title>Omega Series - Defenders</title>
  <plot>A animated series from the local tvshow.nfo.</plot>
  <genre>Action</genre>
</tvshow>`)
	writeFile(t, filepath.Join(seriesDir, "poster.jpg"), "fake-poster-bytes")

	if err := s.RefreshItem(context.Background(), series.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	after, err := st.GetItem(context.Background(), series.ID)
	if err != nil {
		t.Fatalf("GetItem after RefreshItem: %v", err)
	}
	wantOverview := "A animated series from the local tvshow.nfo."
	if after.Overview != wantOverview {
		t.Errorf("Overview after RefreshItem = %q, want %q (local tvshow.nfo must be re-read)", after.Overview, wantOverview)
	}

	refs, err := st.GetImageRefs(context.Background(), series.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(image refs) = %d, want 1 (local poster.jpg picked up by RefreshItem)", len(refs))
	}
}

// TestScanLibraryAppliesNamedSeason proves a fresh scan picks up a
// <namedseason> override from the series-root tvshow.nfo onto the matching
// Season item's Name — validated against the reference server's
// SeriesNfoSeasonProvider/SeriesNfoSeasonParser (docs/08's "Local-vs-remote
// precedence" section): every other <tvshow> field in that same file
// (title, plot, ...) is series-level and must NOT leak onto the Season
// item, so this also asserts the Season's Name is the override, not the
// series' own title.
func TestScanLibraryAppliesNamedSeason(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Omega Series")
	base := filepath.Join(seriesDir, "Season 01")
	writeFile(t, filepath.Join(base, "Omega Series - S01E01 - Pilot.mkv"), "ep1")
	writeFile(t, filepath.Join(seriesDir, "tvshow.nfo"), `<tvshow>
  <title>Omega Series - Defenders</title>
  <namedseason number="1">Temporada Especial</namedseason>
</tvshow>`)

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{
		ID: "lib-namedseason", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	seasonPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeason}, Limit: 10,
	})
	if err != nil || len(seasonPage.Items) != 1 {
		t.Fatalf("season query: err=%v, len=%d, want 1", err, len(seasonPage.Items))
	}
	if got, want := seasonPage.Items[0].Name, "Temporada Especial"; got != want {
		t.Errorf("season Name = %q, want %q (from <namedseason>)", got, want)
	}
}

// TestRefreshItemSeasonAppliesNamedSeason is TestScanLibraryAppliesNamedSeason's
// per-item-refresh counterpart, and also proves the season's own folder
// takes priority over the series root when both have a tvshow.nfo — the
// same lookup order as the reference server's SeriesNfoSeasonProvider.
func TestRefreshItemSeasonAppliesNamedSeason(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Omega Series")
	base := filepath.Join(seriesDir, "Season 01")
	writeFile(t, filepath.Join(base, "Omega Series - S01E01 - Pilot.mkv"), "ep1")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{
		ID: "lib-namedseason-refresh", Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	seasonPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeason}, Limit: 10,
	})
	if err != nil || len(seasonPage.Items) != 1 {
		t.Fatalf("season query: err=%v, len=%d, want 1", err, len(seasonPage.Items))
	}
	season := seasonPage.Items[0]

	// A series-root tvshow.nfo with a <namedseason> for a season that has no
	// tvshow.nfo of its own — the fallback path.
	writeFile(t, filepath.Join(seriesDir, "tvshow.nfo"), `<tvshow>
  <namedseason number="1">From Series Root</namedseason>
</tvshow>`)
	if err := s.RefreshItem(context.Background(), season.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	after, err := st.GetItem(context.Background(), season.ID)
	if err != nil {
		t.Fatalf("GetItem after RefreshItem: %v", err)
	}
	if got, want := after.Name, "From Series Root"; got != want {
		t.Errorf("season Name = %q, want %q (fallback to series-root tvshow.nfo)", got, want)
	}

	// The season's own folder gaining a tvshow.nfo must win over the
	// series-root one from here on.
	writeFile(t, filepath.Join(base, "tvshow.nfo"), `<tvshow>
  <namedseason number="1">From Season Folder</namedseason>
</tvshow>`)
	if err := s.RefreshItem(context.Background(), season.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}
	after, err = st.GetItem(context.Background(), season.ID)
	if err != nil {
		t.Fatalf("GetItem after second RefreshItem: %v", err)
	}
	if got, want := after.Name, "From Season Folder"; got != want {
		t.Errorf("season Name = %q, want %q (season's own folder must win over series root)", got, want)
	}
}

// TestRefreshItemMovieAppliesLocalNFOAndArtwork is TestRefreshItemMovie's
// counterpart for the local (not remote-provider) re-read path: a movie's
// own folder gaining a movie.nfo/poster.jpg must be picked up by
// RefreshItem too, not just by a full ScanLibrary pass.
func TestRefreshItemMovieAppliesLocalNFOAndArtwork(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(movieDir, "movie.mkv"), "not a real video, just enough to exist")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{
		ID: "lib-movies-nfo", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	writeFile(t, filepath.Join(movieDir, "movie.nfo"), `<movie>
  <title>The Matrix</title>
  <plot>Overview from a .nfo added after the initial scan.</plot>
</movie>`)
	writeFile(t, filepath.Join(movieDir, "poster.jpg"), "fake-poster-bytes")

	if err := s.RefreshItem(context.Background(), item.ID, false); err != nil {
		t.Fatalf("RefreshItem: %v", err)
	}

	after, err := st.GetItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetItem after RefreshItem: %v", err)
	}
	want := "Overview from a .nfo added after the initial scan."
	if after.Overview != want {
		t.Errorf("Overview after RefreshItem = %q, want %q (local movie.nfo must be re-read)", after.Overview, want)
	}

	refs, err := st.GetImageRefs(context.Background(), item.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(image refs) = %d, want 1 (local poster.jpg picked up by RefreshItem)", len(refs))
	}
}

// TestRefreshItemImagesRespectReplaceAllImages validates the fix for a real
// divergence from the reference server found by reading its actual
// ItemImageProvider source (docs/08's "Local-vs-remote precedence"
// section): a Provider-returned image must never overwrite an image kind
// the item already has unless the caller explicitly asked to replace it —
// Tinyfin used to always let the Provider win regardless.
func TestRefreshItemImagesRespectReplaceAllImages(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(movieDir, "movie.mkv"), "not a real video, just enough to exist")
	writeFile(t, filepath.Join(movieDir, "poster.jpg"), "local-poster-bytes")

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testPosterBytes(t))
	}))
	defer imgSrv.Close()

	st := newTestStore(t)
	provider := &fakeProvider{imageURL: imgSrv.URL + "/poster.jpg"}
	imageDir := t.TempDir()
	s := New(st, provider, Config{ImageDir: imageDir, ImageHosts: imageHostsFor(imgSrv)})

	lib := storage.Library{
		ID: "lib-replace-images", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: err=%v, len=%d, want 1", err, len(page.Items))
	}
	item := page.Items[0]

	assertPrimaryBasename := func(want string) {
		t.Helper()
		refs, err := st.GetImageRefs(context.Background(), item.ID, "Primary")
		if err != nil {
			t.Fatalf("GetImageRefs: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("len(Primary image refs) = %d, want 1", len(refs))
		}
		if got := filepath.Base(refs[0].Path); got != want {
			t.Errorf("Primary image basename = %q, want %q", got, want)
		}
	}

	// A fresh ScanLibrary pass already seeds the local poster.jpg before
	// enrich runs, so even a first scan must not let the Provider overwrite
	// it (ScanLibrary always calls enrich with replaceImages=false).
	assertPrimaryBasename("poster.jpg")

	// RefreshItem(..., false) — the caller didn't ask to replace images
	// (ReplaceAllImages=false, jellyfin-web's dashboard action's own
	// default) — must leave the existing local image alone even though the
	// Provider has a different one to offer.
	if err := s.RefreshItem(context.Background(), item.ID, false); err != nil {
		t.Fatalf("RefreshItem(replaceImages=false): %v", err)
	}
	assertPrimaryBasename("poster.jpg")

	// RefreshItem(..., true) — an explicit ReplaceAllImages=true request —
	// must let the Provider's image win.
	if err := s.RefreshItem(context.Background(), item.ID, true); err != nil {
		t.Fatalf("RefreshItem(replaceImages=true): %v", err)
	}
	refs, err := st.GetImageRefs(context.Background(), item.ID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(Primary image refs) = %d, want 1", len(refs))
	}
	if filepath.Base(refs[0].Path) == "poster.jpg" {
		t.Error("Primary image still the local poster.jpg after RefreshItem(replaceImages=true), want the Provider's image")
	}
	if _, err := os.Stat(refs[0].Path); err != nil {
		t.Errorf("downloaded provider image file missing at %s: %v", refs[0].Path, err)
	}
}

// testPosterBytes is a real 400x600 (2:3 poster) PNG. A provider image has to
// be decodable to have dimensions, and it has to sniff as an image at all: the
// scan's download goes through internal/imagefetch, which refuses a body that is
// not an image (and, by default, any non-public address — hence imageHostsFor).
func testPosterBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	draw(img, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// imageHostsFor is the allowlist a test needs for its own httptest server: the
// download guard refuses loopback unless the operator names it, which is the
// production default these tests must not weaken.
func imageHostsFor(imgSrv *httptest.Server) []string {
	u, err := url.Parse(imgSrv.URL)
	if err != nil {
		return nil
	}
	return []string{u.Hostname()}
}

// draw fills every pixel of img with c — image/draw would pull in another
// import for one call this test only needs once.
func draw(img *image.RGBA, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}

// TestScanLibraryRecordsRootFolder proves each scanned library path is
// cataloged as a plain Folder item named after its directory: a recursive
// listing of the library includes it (items.list.R7), while the library's
// direct children (parentless, non-Folder items) do not. A rescan keeps the
// same item.
func TestScanLibraryRecordsRootFolder(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Films")
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "x")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{ImageDir: t.TempDir()})
	lib := storage.Library{ID: "lib-movies", Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	folders := func() []storage.Item {
		t.Helper()
		page, err := st.QueryItems(context.Background(), storage.ItemFilter{
			LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeFolder}, Limit: 10,
		})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		return page.Items
	}

	for pass := 0; pass < 2; pass++ {
		if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
			t.Fatalf("ScanLibrary: %v", err)
		}
		got := folders()
		if len(got) != 1 {
			t.Fatalf("pass %d: %d Folder items, want 1", pass, len(got))
		}
		if got[0].Name != "Films" || got[0].Path != root || got[0].ParentID != nil {
			t.Errorf("pass %d: folder = %+v, want Name Films, Path %s, no parent", pass, got[0], root)
		}
	}

	top, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, TopLevelOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(top.Items) != 1 || top.Items[0].Type != storage.ItemTypeMovie {
		t.Errorf("top-level items = %+v, want just the movie", top.Items)
	}
}
