package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// These three routes aren't wired into server.go's route table yet
// (registering them there is left to a separate change, per this task's
// scope — see library_admin_test.go's callHandlerDirect doc comment for the
// same situation with the LibraryOptions/Name endpoints), so their handlers
// are exercised directly rather than through an httptest.Server round trip.

func TestLocalizationCultures(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleLocalizationCultures, http.MethodGet, "/Localization/cultures", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got []culture
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("cultures list is empty")
	}

	var english *culture
	for i := range got {
		if got[i].TwoLetterISOLanguageName == "en" {
			english = &got[i]
			break
		}
	}
	if english == nil {
		t.Fatal("English (two-letter code \"en\") not found in cultures list")
	}
	if english.DisplayName != "English" {
		t.Errorf("DisplayName = %q, want %q", english.DisplayName, "English")
	}
	if english.ThreeLetterISOLanguageName != "eng" {
		t.Errorf("ThreeLetterISOLanguageName = %q, want %q", english.ThreeLetterISOLanguageName, "eng")
	}
	if len(english.ThreeLetterISOLanguageNames) == 0 {
		t.Error("ThreeLetterISOLanguageNames is empty, want at least one entry")
	}
}

func TestLocalizationCountries(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleLocalizationCountries, http.MethodGet, "/Localization/countries", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got []country
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("countries list is empty")
	}

	var us *country
	for i := range got {
		if got[i].TwoLetterISORegionName == "US" {
			us = &got[i]
			break
		}
	}
	if us == nil {
		t.Fatal("United States (two-letter code \"US\") not found in countries list")
	}
	if us.DisplayName != "United States" {
		t.Errorf("DisplayName = %q, want %q", us.DisplayName, "United States")
	}
	if us.ThreeLetterISORegionName != "USA" {
		t.Errorf("ThreeLetterISORegionName = %q, want %q", us.ThreeLetterISORegionName, "USA")
	}
}

// TestLibrariesAvailableOptions_Movies covers the shape jellyfin-web's
// library-edit form requests for a movie library: exactly one TypeOptions
// entry ("Movie"), with TheMovieDb as both metadata and image fetcher —
// this project's one real metadata.Provider (internal/metadata/tmdb.go).
func TestLibrariesAvailableOptions_Movies(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleLibrariesAvailableOptions, http.MethodGet, "/Libraries/AvailableOptions?IsNewLibrary=false&LibraryContentType=movies", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got libraryAvailableOptions
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.TypeOptions) != 1 || got.TypeOptions[0].Type != "Movie" {
		t.Fatalf("TypeOptions = %+v, want exactly one entry of Type \"Movie\"", got.TypeOptions)
	}
	movie := got.TypeOptions[0]
	if len(movie.MetadataFetchers) != 1 || movie.MetadataFetchers[0].Name != "TheMovieDb" {
		t.Errorf("MetadataFetchers = %+v, want exactly [TheMovieDb]", movie.MetadataFetchers)
	}
	if len(movie.ImageFetchers) != 1 || movie.ImageFetchers[0].Name != "TheMovieDb" {
		t.Errorf("ImageFetchers = %+v, want exactly [TheMovieDb]", movie.ImageFetchers)
	}
	if len(got.MetadataReaders) != 1 || got.MetadataReaders[0].Name != "Nfo" {
		t.Errorf("MetadataReaders = %+v, want exactly [Nfo]", got.MetadataReaders)
	}
	if len(got.MetadataSavers) != 0 {
		t.Errorf("MetadataSavers = %+v, want empty (nothing in this project writes a sidecar file)", got.MetadataSavers)
	}
}

// TestLibrariesAvailableOptions_NoContentTypeDefaultsToMixed covers the
// unset-LibraryContentType case (confirmed live to behave the same as
// LibraryContentType=mixed): Series/Season/Episode/Movie, in that order.
func TestLibrariesAvailableOptions_NoContentTypeDefaultsToMixed(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleLibrariesAvailableOptions, http.MethodGet, "/Libraries/AvailableOptions?IsNewLibrary=false", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got libraryAvailableOptions
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	want := []string{"Series", "Season", "Episode", "Movie"}
	if len(got.TypeOptions) != len(want) {
		t.Fatalf("len(TypeOptions) = %d, want %d (%+v)", len(got.TypeOptions), len(want), got.TypeOptions)
	}
	for i, ty := range want {
		if got.TypeOptions[i].Type != ty {
			t.Errorf("TypeOptions[%d].Type = %q, want %q", i, got.TypeOptions[i].Type, ty)
		}
	}
}

// TestLibrariesAvailableOptions_MusicHasNoFetchers covers the "minimal over
// guessed" scope decision: this project has no music metadata provider (no
// MusicBrainz/TheAudioDB, unlike the reference server), so every
// MusicArtist/MusicAlbum/Audio/MusicVideo entry must come back with empty
// fetcher lists rather than an invented capability.
func TestLibrariesAvailableOptions_MusicHasNoFetchers(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleLibrariesAvailableOptions, http.MethodGet, "/Libraries/AvailableOptions?IsNewLibrary=false&LibraryContentType=music", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got libraryAvailableOptions
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.TypeOptions) == 0 {
		t.Fatal("TypeOptions is empty")
	}
	for _, to := range got.TypeOptions {
		if len(to.MetadataFetchers) != 0 {
			t.Errorf("Type %q: MetadataFetchers = %+v, want empty", to.Type, to.MetadataFetchers)
		}
		if len(to.ImageFetchers) != 0 {
			t.Errorf("Type %q: ImageFetchers = %+v, want empty", to.Type, to.ImageFetchers)
		}
	}
	if len(got.MetadataReaders) != 0 {
		t.Errorf("MetadataReaders = %+v, want empty (no .nfo support for music items)", got.MetadataReaders)
	}
}
