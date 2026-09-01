package scan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
		return metadata.Result{Overview: "A long-running anime.", ExternalIDs: map[string]string{"Tmdb": "12"}}, nil
	case metadata.KindEpisode:
		return metadata.Result{Overview: "An episode overview."}, nil
	}
	return metadata.Result{}, metadata.ErrNotFound
}

func TestScanLibraryMovies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999)", "movie.mkv"), "not a real video, just enough to exist")

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer imgSrv.Close()

	st := newTestStore(t)
	provider := &fakeProvider{imageURL: imgSrv.URL + "/poster.jpg"}
	imageDir := t.TempDir()
	s := New(st, provider, Config{ImageDir: imageDir})

	lib := storage.Library{
		ID: "lib-movies", Name: "Filmes", CollectionType: storage.CollectionTypeMovies, Paths: []string{root},
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
	if item.Name != "The Matrix" {
		t.Errorf("Name = %q, want %q", item.Name, "The Matrix")
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

func TestScanLibraryTVShows(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "Dragon Ball (1986)", "Season 01")
	writeFile(t, filepath.Join(base, "Dragon Ball - S01E01 - The Secret of the Dragon Balls.mkv"), "ep1")
	writeFile(t, filepath.Join(base, "Dragon Ball - S01E02 - Second Episode.mkv"), "ep2")
	// A non-matching file in the season folder should be skipped, not crash the scan.
	writeFile(t, filepath.Join(base, "notes.txt"), "irrelevant")

	st := newTestStore(t)
	provider := &fakeProvider{}
	s := New(st, provider, Config{})

	lib := storage.Library{
		ID: "lib-tv", Name: "Séries", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root},
	}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}

	stats, err := s.ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	// 1 series + 2 episodes go through the bounded job pool (season upserts
	// happen synchronously during the walk, not as pool jobs — see scan.go).
	if stats.ItemsScanned != 3 {
		t.Fatalf("ItemsScanned = %d, want 3 (1 series + 2 episodes); errors: %v", stats.ItemsScanned, stats.Errors)
	}

	seriesPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeSeries}, Limit: 10,
	})
	if err != nil || len(seriesPage.Items) != 1 {
		t.Fatalf("series query: err=%v, len=%d, want 1", err, len(seriesPage.Items))
	}
	series := seriesPage.Items[0]
	if series.Name != "Dragon Ball" {
		t.Errorf("series.Name = %q, want %q", series.Name, "Dragon Ball")
	}
	if series.Overview == "" {
		t.Error("series.Overview is empty, want provider enrichment applied")
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

	epPage, err := st.QueryItems(context.Background(), storage.ItemFilter{
		LibraryID: &lib.ID, ParentID: &season.ID, ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}, Limit: 10,
		SortBy: []storage.SortField{{By: storage.SortByIndexNumber}},
	})
	if err != nil || len(epPage.Items) != 2 {
		t.Fatalf("episode query: err=%v, len=%d, want 2", err, len(epPage.Items))
	}
	ep1 := epPage.Items[0]
	if ep1.Name != "The Secret of the Dragon Balls" {
		t.Errorf("ep1.Name = %q, want %q", ep1.Name, "The Secret of the Dragon Balls")
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

func TestScanLibraryNoProviderStillCatalogs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Some Movie (2020)", "movie.mkv"), "content")

	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})

	lib := storage.Library{ID: "lib-1", Name: "Filmes", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
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

	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Some Movie" {
		t.Fatalf("Items = %+v, want one item named %q", page.Items, "Some Movie")
	}
}
