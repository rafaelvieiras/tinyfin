package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestDateLastMediaAddedMatchesTheNewestAddition: a Series reports the newest
// addition inside it, a Season and a library root report the zero time, a Movie
// reports nothing, and the member only shows up when Fields asks for it —
// all measured on the reference (items.latest.R6).
func TestDateLastMediaAddedMatchesTheNewestAddition(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "fran", "franpass")
	token := loginToken(t, ts.URL, user.Name, "franpass")
	get := authedClient(t, ts.URL, token)

	now := time.Now().UTC().Truncate(time.Second)
	showsLib := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	seriesID := upsertSeries(t, st, showsLib, "Dated Show", now.Add(-72*time.Hour))
	seasonID := upsertSeason(t, st, showsLib, seriesID, "Season 1", 1, now.Add(-72*time.Hour))
	upsertEpisode(t, st, showsLib, seriesID, seasonID, "D1", 1, now.Add(-20*time.Hour))
	newest := now.Add(-4 * time.Hour)
	upsertEpisode(t, st, showsLib, seriesID, seasonID, "D2", 2, newest)

	asked := itemByID(t, get, "/Items?parentId="+string(showsLib)+"&recursive=true&includeItemTypes=Series&fields=DateLastMediaAdded", string(seriesID))
	if asked.DateLastMediaAdded == nil {
		t.Fatalf("DateLastMediaAdded missing from a Series when Fields asked for it: %+v", asked)
	}
	if !asked.DateLastMediaAdded.Time.Equal(newest) {
		t.Fatalf("DateLastMediaAdded = %v, want the newest episode's %v", asked.DateLastMediaAdded.Time, newest)
	}
	if !asked.DateCreated.Time.Equal(now.Add(-72 * time.Hour)) {
		t.Fatalf("DateCreated = %v, want the series' own %v", asked.DateCreated.Time, now.Add(-72*time.Hour))
	}

	if quiet := itemByID(t, get, "/Items?parentId="+string(showsLib)+"&recursive=true&includeItemTypes=Series", string(seriesID)); quiet.DateLastMediaAdded != nil {
		t.Fatalf("DateLastMediaAdded must stay out unless Fields asks for it: %+v", quiet.DateLastMediaAdded)
	}

	if season := itemByID(t, get, "/Items?parentId="+string(showsLib)+"&recursive=true&includeItemTypes=Season&fields=DateLastMediaAdded", string(seasonID)); season.DateLastMediaAdded == nil || !season.DateLastMediaAdded.Time.IsZero() {
		t.Fatalf("a Season's DateLastMediaAdded = %+v, want the zero time", season.DateLastMediaAdded)
	}

	moviesLib := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	movieID := upsertMovie(t, st, moviesLib, "Plain Movie", now.Add(-time.Hour))
	if movie := itemByID(t, get, "/Items?parentId="+string(moviesLib)+"&recursive=true&includeItemTypes=Movie&fields=DateLastMediaAdded", string(movieID)); movie.DateLastMediaAdded != nil {
		t.Fatalf("a Movie must not report DateLastMediaAdded: %+v", movie.DateLastMediaAdded)
	}

	// The latest row publishes it too when asked (the home screen's shape).
	row := latestRow(t, get, "?limit=50&fields=DateLastMediaAdded")
	for _, it := range row {
		if it.Type == "Series" && it.DateLastMediaAdded == nil {
			t.Fatalf("/Items/Latest dropped DateLastMediaAdded from a Series card: %+v", it)
		}
	}
}

// itemByID finds one DTO in a listing response by id.
func itemByID(t *testing.T, get func(method, path string) *http.Response, path, id string) baseItemDto {
	t.Helper()
	resp := get(http.MethodGet, path)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
	}
	var page baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	for _, it := range page.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("%s has no item %s (%d items)", path, id, len(page.Items))
	return baseItemDto{}
}
