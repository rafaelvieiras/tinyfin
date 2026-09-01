package scan

import (
	"encoding/xml"
	"testing"
	"time"
)

func TestMovieNFOParsing(t *testing.T) {
	cases := []struct {
		name    string
		xmlData string
		wantErr bool
		check   func(t *testing.T, n movieNFO)
	}{
		{
			name: "complete movie",
			xmlData: `<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>The Matrix</title>
  <originaltitle>The Matrix</originaltitle>
  <plot>A hacker discovers reality is a simulation.</plot>
  <year>1999</year>
  <premiered>1999-03-31</premiered>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <tag>cyberpunk</tag>
  <tag>dystopia</tag>
  <mpaa>R</mpaa>
  <ratings>
    <rating name="themoviedb" max="10">
      <value>8.7</value>
    </rating>
  </ratings>
  <uniqueid type="tmdb">603</uniqueid>
  <uniqueid type="imdb">tt0133093</uniqueid>
</movie>`,
			check: func(t *testing.T, n movieNFO) {
				if n.Title != "The Matrix" {
					t.Errorf("Title = %q, want %q", n.Title, "The Matrix")
				}
				if n.OriginalTitle != "The Matrix" {
					t.Errorf("OriginalTitle = %q", n.OriginalTitle)
				}
				if n.Plot != "A hacker discovers reality is a simulation." {
					t.Errorf("Plot = %q", n.Plot)
				}
				if n.Year != 1999 {
					t.Errorf("Year = %d, want 1999", n.Year)
				}
				if n.Premiered != "1999-03-31" {
					t.Errorf("Premiered = %q", n.Premiered)
				}
				if len(n.Genres) != 2 || n.Genres[0] != "Action" || n.Genres[1] != "Science Fiction" {
					t.Errorf("Genres = %v", n.Genres)
				}
				if len(n.Tags) != 2 || n.Tags[0] != "cyberpunk" || n.Tags[1] != "dystopia" {
					t.Errorf("Tags = %v", n.Tags)
				}
				if n.MPAA != "R" {
					t.Errorf("MPAA = %q", n.MPAA)
				}
				r, ok := nfoRating(n.commonNFOFields)
				if !ok || r != 8.7 {
					t.Errorf("nfoRating = %v, %v; want 8.7, true", r, ok)
				}
				id, ok := nfoProviderID(n.UniqueIDs, "tmdb")
				if !ok || id != "603" {
					t.Errorf("nfoProviderID(tmdb) = %v, %v; want 603, true", id, ok)
				}
			},
		},
		{
			name: "missing tags leave zero values",
			xmlData: `<movie>
  <title>Some Movie</title>
</movie>`,
			check: func(t *testing.T, n movieNFO) {
				if n.Title != "Some Movie" {
					t.Errorf("Title = %q", n.Title)
				}
				if n.Plot != "" {
					t.Errorf("Plot = %q, want empty", n.Plot)
				}
				if n.Year != 0 {
					t.Errorf("Year = %d, want 0", n.Year)
				}
				if len(n.Genres) != 0 {
					t.Errorf("Genres = %v, want empty", n.Genres)
				}
				if _, ok := nfoRating(n.commonNFOFields); ok {
					t.Error("nfoRating ok, want false (no rating tags)")
				}
				if _, ok := nfoProviderID(n.UniqueIDs, "tmdb"); ok {
					t.Error("nfoProviderID ok, want false (no uniqueid tags)")
				}
			},
		},
		{
			name: "flat rating tag used when no ratings block",
			xmlData: `<movie>
  <title>Old Style</title>
  <rating>7.5</rating>
</movie>`,
			check: func(t *testing.T, n movieNFO) {
				r, ok := nfoRating(n.commonNFOFields)
				if !ok || r != 7.5 {
					t.Errorf("nfoRating = %v, %v; want 7.5, true", r, ok)
				}
			},
		},
		{
			name:    "malformed xml",
			xmlData: `<movie><title>Broken</title never closed`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n movieNFO
			err := xml.Unmarshal([]byte(tc.xmlData), &n)
			if tc.wantErr {
				if err == nil {
					t.Fatal("Unmarshal: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			tc.check(t, n)
		})
	}
}

func TestTVShowNFOParsing(t *testing.T) {
	data := `<tvshow>
  <title>Dragon Ball</title>
  <plot>A young monkey-tailed boy searches for the seven dragon balls.</plot>
  <year>1986</year>
  <genre>Anime</genre>
  <genre>Adventure</genre>
  <uniqueid type="tmdb">12</uniqueid>
</tvshow>`
	var n tvshowNFO
	if err := xml.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if n.Title != "Dragon Ball" {
		t.Errorf("Title = %q", n.Title)
	}
	if n.Plot == "" {
		t.Error("Plot is empty")
	}
	if n.Year != 1986 {
		t.Errorf("Year = %d, want 1986", n.Year)
	}
	if len(n.Genres) != 2 {
		t.Errorf("Genres = %v, want 2 entries", n.Genres)
	}
	id, ok := nfoProviderID(n.UniqueIDs, "tmdb")
	if !ok || id != "12" {
		t.Errorf("nfoProviderID(tmdb) = %v, %v; want 12, true", id, ok)
	}
}

func TestEpisodeNFOParsing(t *testing.T) {
	data := `<episodedetails>
  <title>The Secret of the Dragon Balls</title>
  <plot>Bulma meets Goku.</plot>
  <aired>1986-02-26</aired>
  <season>1</season>
  <episode>1</episode>
</episodedetails>`
	var n episodeNFO
	if err := xml.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if n.Title != "The Secret of the Dragon Balls" {
		t.Errorf("Title = %q", n.Title)
	}
	if n.Plot != "Bulma meets Goku." {
		t.Errorf("Plot = %q", n.Plot)
	}
	if n.Season != 1 || n.Episode != 1 {
		t.Errorf("Season/Episode = %d/%d, want 1/1", n.Season, n.Episode)
	}
	aired, ok := parseNFODate(n.Aired)
	if !ok || !aired.Equal(time.Date(1986, 2, 26, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parseNFODate(Aired) = %v, %v; want 1986-02-26, true", aired, ok)
	}
}

func TestParseNFODate(t *testing.T) {
	cases := []struct {
		in     string
		want   time.Time
		wantOK bool
	}{
		{"1999-03-31", time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC), true},
		{"", time.Time{}, false},
		{"not-a-date", time.Time{}, false},
		{"1999/03/31", time.Time{}, false},
	}
	for _, tc := range cases {
		got, ok := parseNFODate(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parseNFODate(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && !got.Equal(tc.want) {
			t.Errorf("parseNFODate(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
