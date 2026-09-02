package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestTMDBProvider points a TMDBProvider at a local httptest.Server
// instead of the real TMDB API — no network access, no real API key
// needed, deterministic canned responses.
func newTestTMDBProvider(t *testing.T, mux *http.ServeMux) *TMDBProvider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := NewTMDBProvider("test-key")
	p.baseURL = srv.URL
	return p
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encoding test response: %v", err)
	}
}

func TestTMDBProviderLookupMovie(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "test-key" {
			t.Errorf("search/movie api_key = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("query"); got != "The Matrix" {
			t.Errorf("search/movie query = %q, want %q", got, "The Matrix")
		}
		if got := r.URL.Query().Get("year"); got != "1999" {
			t.Errorf("search/movie year = %q, want 1999", got)
		}
		writeJSON(t, w, tmdbSearchResponse{Results: []tmdbSearchResult{{ID: 603, Title: "The Matrix"}}})
	})
	mux.HandleFunc("/movie/603", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, tmdbDetailsResponse{
			tmdbSearchResult: tmdbSearchResult{
				ID: 603, Title: "The Matrix", OriginalTitle: "The Matrix",
				Overview:    "A hacker discovers reality is a simulation.",
				ReleaseDate: "1999-03-31", VoteAverage: 8.2,
				PosterPath: "/poster.jpg", BackdropPath: "/backdrop.jpg",
			},
			Genres: []tmdbGenre{{Name: "Action"}, {Name: "Science Fiction"}},
		})
	})

	p := newTestTMDBProvider(t, mux)
	res, err := p.Lookup(context.Background(), Query{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Title != "The Matrix" {
		t.Errorf("Title = %q, want %q", res.Title, "The Matrix")
	}
	if res.Overview == "" {
		t.Error("Overview is empty")
	}
	if len(res.Genres) != 2 {
		t.Errorf("len(Genres) = %d, want 2", len(res.Genres))
	}
	if res.ExternalIDs["Tmdb"] != "603" {
		t.Errorf("ExternalIDs[Tmdb] = %q, want 603", res.ExternalIDs["Tmdb"])
	}
	if res.PremiereDateUnix == 0 {
		t.Error("PremiereDateUnix is zero, want the parsed release date")
	}
	if len(res.Images) != 2 {
		t.Fatalf("len(Images) = %d, want 2", len(res.Images))
	}
	if res.Images[0].URL != tmdbImageBase+"/poster.jpg" {
		t.Errorf("Images[0].URL = %q, want %q", res.Images[0].URL, tmdbImageBase+"/poster.jpg")
	}
}

// TestTMDBProviderLookupMovieIncludesCredits proves lookupMovie's
// append_to_response=credits integration maps TMDB's cast/crew shape into
// Result.People: cast becomes Actor entries in billing order, and only the
// crew jobs crewTypeForJob recognizes (Director/Writer family/Producer)
// come through — everything else is dropped.
func TestTMDBProviderLookupMovieIncludesCredits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, tmdbSearchResponse{Results: []tmdbSearchResult{{ID: 603, Title: "The Matrix"}}})
	})
	mux.HandleFunc("/movie/603", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("append_to_response"); got != "credits" {
			t.Errorf("append_to_response = %q, want credits", got)
		}
		writeJSON(t, w, tmdbDetailsResponse{
			tmdbSearchResult: tmdbSearchResult{ID: 603, Title: "The Matrix", ReleaseDate: "1999-03-31"},
			Credits: tmdbCredits{
				Cast: []tmdbCastMember{
					{ID: 8, Name: "Carrie-Anne Moss", Character: "Trinity", Order: 1},
					{ID: 6384, Name: "Keanu Reeves", Character: "Neo", Order: 0},
				},
				Crew: []tmdbCrewMember{
					{ID: 9, Name: "Lana Wachowski", Job: "Director"},
					{ID: 10, Name: "Lilly Wachowski", Job: "Writer"},
					{ID: 11, Name: "Joel Silver", Job: "Producer"},
					{ID: 12, Name: "Bill Pope", Job: "Director of Photography"},
				},
			},
		})
	})

	p := newTestTMDBProvider(t, mux)
	res, err := p.Lookup(context.Background(), Query{Kind: KindMovie, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	// 2 cast (Actor, billing order) + 3 recognized crew jobs; "Director of
	// Photography" is dropped.
	if len(res.People) != 5 {
		t.Fatalf("len(People) = %d, want 5; got %+v", len(res.People), res.People)
	}
	if got := res.People[0]; got.Name != "Keanu Reeves" || got.Role != "Neo" || got.Type != "Actor" || got.TMDBID != 6384 {
		t.Errorf("People[0] = %+v, want {Keanu Reeves, Neo, Actor, 6384} (billing order 0 first)", got)
	}
	if got := res.People[1]; got.Name != "Carrie-Anne Moss" || got.Role != "Trinity" || got.Type != "Actor" {
		t.Errorf("People[1] = %+v, want {Carrie-Anne Moss, Trinity, Actor, ...}", got)
	}
	var gotTypes []string
	for _, p := range res.People[2:] {
		gotTypes = append(gotTypes, p.Type)
	}
	wantTypes := []string{"Director", "Writer", "Producer"}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("crew types = %v, want %v", gotTypes, wantTypes)
	}
	for i, wt := range wantTypes {
		if gotTypes[i] != wt {
			t.Errorf("crew[%d].Type = %q, want %q", i, gotTypes[i], wt)
		}
	}
}

// TestCreditsToPeopleCapsCastAndOrdersByBilling proves creditsToPeople
// bounds cast to maxCastCredits and sorts by TMDB's "order" field
// regardless of input order.
func TestCreditsToPeopleCapsCastAndOrdersByBilling(t *testing.T) {
	var cast []tmdbCastMember
	for i := 0; i < maxCastCredits+5; i++ {
		// Deliberately reversed input order so a passing test proves
		// creditsToPeople sorts, not just passes input through.
		order := maxCastCredits + 5 - i
		cast = append(cast, tmdbCastMember{Name: fmt.Sprintf("Actor %d", order), Order: order})
	}
	people := creditsToPeople(tmdbCredits{Cast: cast})
	if len(people) != maxCastCredits {
		t.Fatalf("len(people) = %d, want %d (capped)", len(people), maxCastCredits)
	}
	if people[0].Name != "Actor 1" {
		t.Errorf("people[0].Name = %q, want %q (lowest Order first)", people[0].Name, "Actor 1")
	}
	for i, p := range people {
		wantName := fmt.Sprintf("Actor %d", i+1)
		if p.Name != wantName {
			t.Errorf("people[%d].Name = %q, want %q", i, p.Name, wantName)
		}
		if p.Type != "Actor" {
			t.Errorf("people[%d].Type = %q, want Actor", i, p.Type)
		}
	}
}

func TestTMDBProviderLookupMovieNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, tmdbSearchResponse{Results: nil})
	})

	p := newTestTMDBProvider(t, mux)
	_, err := p.Lookup(context.Background(), Query{Kind: KindMovie, Title: "Some Obscure Title Nobody Made", Year: 1900})
	if err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTMDBProviderLookupEpisodeUsesKnownSeriesID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		t.Error("search/tv should not be called when ExternalIDs already has a Tmdb series ID")
	})
	mux.HandleFunc("/tv/1396/season/1/episode/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, tmdbEpisodeResponse{
			Name: "Pilot", Overview: "A high school chemistry teacher...",
			AirDate: "2008-01-20", VoteAverage: 9.0, StillPath: "/still.jpg",
		})
	})

	p := newTestTMDBProvider(t, mux)
	res, err := p.Lookup(context.Background(), Query{
		Kind: KindEpisode, SeriesTitle: "Breaking Bad", SeasonNumber: 1, EpisodeNumber: 1,
		ExternalIDs: map[string]string{"Tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Title != "Pilot" {
		t.Errorf("Title = %q, want %q", res.Title, "Pilot")
	}
	if len(res.Images) != 1 || res.Images[0].Kind != "Primary" {
		t.Errorf("Images = %+v, want one Primary image", res.Images)
	}
}

func TestTMDBProviderLookupEpisodeResolvesSeriesIDWhenUnknown(t *testing.T) {
	mux := http.NewServeMux()
	searchCalled := false
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		searchCalled = true
		writeJSON(t, w, tmdbSearchResponse{Results: []tmdbSearchResult{{ID: 1396, Name: "Breaking Bad"}}})
	})
	mux.HandleFunc("/tv/1396/season/1/episode/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, tmdbEpisodeResponse{Name: "Pilot", Overview: "..."})
	})

	p := newTestTMDBProvider(t, mux)
	_, err := p.Lookup(context.Background(), Query{
		Kind: KindEpisode, SeriesTitle: "Breaking Bad", SeasonNumber: 1, EpisodeNumber: 1,
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !searchCalled {
		t.Error("expected /search/tv to be called when no Tmdb series ID was already known")
	}
}

func TestTMDBProviderLookupRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	p := newTestTMDBProvider(t, mux)
	_, err := p.Lookup(context.Background(), Query{Kind: KindMovie, Title: "X", Year: 2000})
	var pe *ProviderError
	if err == nil {
		t.Fatal("err = nil, want a *ProviderError")
	}
	if !asProviderError(err, &pe) || pe.Code != "rate_limited" {
		t.Errorf("err = %v, want ProviderError{Code: rate_limited}", err)
	}
}

func asProviderError(err error, target **ProviderError) bool {
	pe, ok := err.(*ProviderError)
	if !ok {
		return false
	}
	*target = pe
	return true
}
