package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestItemsCounts isn't wired into server.go's route table yet (registering
// it there is left to a separate change, per this task's scope — see
// library_admin_test.go's callHandlerDirect doc comment for the same
// situation with the LibraryOptions/Name endpoints), so the handler is
// exercised directly rather than through an httptest.Server round trip.

// TestItemsCounts_MoviesSeriesEpisodes covers the one real query path: a
// library with movies, a series, and episodes reports accurate non-zero
// counts for exactly those three fields, while every field this project has
// no scanning pipeline behind stays a structural zero (itemCounts' doc
// comment in items_counts.go).
func TestItemsCounts_MoviesSeriesEpisodes(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	for _, name := range []string{"Movie One", "Movie Two"} {
		if err := st.UpsertItem(context.Background(), storage.Item{
			Type: storage.ItemTypeMovie, LibraryID: libID, Name: name,
		}); err != nil {
			t.Fatalf("UpsertItem(%q): %v", name, err)
		}
	}

	seriesID := storage.ItemID("series-1")
	if err := st.UpsertItem(context.Background(), storage.Item{
		ID: seriesID, Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Test Series",
	}); err != nil {
		t.Fatalf("UpsertItem(series): %v", err)
	}
	for _, name := range []string{"Episode One", "Episode Two", "Episode Three"} {
		if err := st.UpsertItem(context.Background(), storage.Item{
			Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seriesID,
			SeriesID: &seriesID, Name: name,
		}); err != nil {
			t.Fatalf("UpsertItem(%q): %v", name, err)
		}
	}

	w := callHandlerDirect(t, srv.handleItemsCounts, http.MethodGet, "/Items/Counts", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got itemCounts
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if got.MovieCount != 2 {
		t.Errorf("MovieCount = %d, want 2", got.MovieCount)
	}
	if got.SeriesCount != 1 {
		t.Errorf("SeriesCount = %d, want 1", got.SeriesCount)
	}
	if got.EpisodeCount != 3 {
		t.Errorf("EpisodeCount = %d, want 3", got.EpisodeCount)
	}

	// Every other field is a structural zero — no music/book/program/boxset
	// scanning pipeline exists to count anything into them (items_counts.go's
	// doc comment).
	if got.ArtistCount != 0 {
		t.Errorf("ArtistCount = %d, want 0", got.ArtistCount)
	}
	if got.ProgramCount != 0 {
		t.Errorf("ProgramCount = %d, want 0", got.ProgramCount)
	}
	if got.TrailerCount != 0 {
		t.Errorf("TrailerCount = %d, want 0", got.TrailerCount)
	}
	if got.SongCount != 0 {
		t.Errorf("SongCount = %d, want 0", got.SongCount)
	}
	if got.AlbumCount != 0 {
		t.Errorf("AlbumCount = %d, want 0", got.AlbumCount)
	}
	if got.MusicVideoCount != 0 {
		t.Errorf("MusicVideoCount = %d, want 0", got.MusicVideoCount)
	}
	if got.BoxSetCount != 0 {
		t.Errorf("BoxSetCount = %d, want 0", got.BoxSetCount)
	}
	if got.BookCount != 0 {
		t.Errorf("BookCount = %d, want 0", got.BookCount)
	}
	if got.ItemCount != 0 {
		t.Errorf("ItemCount = %d, want 0", got.ItemCount)
	}
}

// TestItemsCounts_EmptyStore covers the no-libraries-at-all case: every
// field comes back 0, not an error — QueryItems on an empty items table is
// a perfectly ordinary zero-match query, not a failure mode.
func TestItemsCounts_EmptyStore(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleItemsCounts, http.MethodGet, "/Items/Counts", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got itemCounts
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	want := itemCounts{}
	if got != want {
		t.Errorf("itemCounts = %+v, want all-zero %+v", got, want)
	}
}
