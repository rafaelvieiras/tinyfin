package scan

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
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
  <title>Example Show</title>
  <plot>A young monkey-tailed boy searches for the seven example orbs.</plot>
  <year>1986</year>
  <genre>Anime</genre>
  <genre>Adventure</genre>
  <uniqueid type="tmdb">12</uniqueid>
</tvshow>`
	var n tvshowNFO
	if err := xml.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if n.Title != "Example Show" {
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
  <title>The Secret of the Example Orbs</title>
  <plot>Bulma meets Goku.</plot>
  <aired>1986-02-26</aired>
  <season>1</season>
  <episode>1</episode>
</episodedetails>`
	var n episodeNFO
	if err := xml.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if n.Title != "The Secret of the Example Orbs" {
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

// writeEpisodeNFO writes data to "<dir>/episode.nfo" and returns the video
// path applyEpisodeNFO expects ("<dir>/episode.mkv", so base+".nfo" resolves
// to the file just written).
func writeEpisodeNFO(t *testing.T, dir, data string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "episode.nfo"), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return filepath.Join(dir, "episode.mkv")
}

// The Portuguese title and plot in the NFO fixtures below are deliberate
// non-English test data (UTF-8 handling in NFO parsing).
// TestApplyEpisodeNFO covers the real bug this task fixes: an
// <episodedetails> .nfo used to only apply Title/Plot/Aired by hand,
// silently dropping every other commonNFOFields tag (ProviderIds, Genres,
// CommunityRating, ProductionYear, ...) since episodeNFO didn't embed
// commonNFOFields. It now does, via applyCommonNFOFields — this asserts
// that wiring end to end through applyEpisodeNFO.
func TestApplyEpisodeNFO(t *testing.T) {
	s := &Scanner{}
	pool := newJobPool(1)

	t.Run("premiered tag populates PremiereDate via commonNFOFields", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<?xml version="1.0" encoding="UTF-8"?>
<episodedetails>
  <title>Vigie os Orbes Antigos</title>
  <plot>Está ficando tarde...</plot>
  <premiered>1986-04-02</premiered>
  <uniqueid type="tmdb" default="true">484774</uniqueid>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if item.Name != "Vigie os Orbes Antigos" {
			t.Errorf("Name = %q", item.Name)
		}
		if item.Overview != "Está ficando tarde..." {
			t.Errorf("Overview = %q", item.Overview)
		}
		if item.PremiereDate == nil || !item.PremiereDate.Equal(time.Date(1986, 4, 2, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("PremiereDate = %v, want 1986-04-02", item.PremiereDate)
		}
		if got := item.ProviderIDs["Tmdb"]; got != "484774" {
			t.Errorf("ProviderIDs[Tmdb] = %q, want 484774", got)
		}
	})

	t.Run("aired tag used as fallback when premiered is absent", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<episodedetails>
  <title>The Secret of the Example Orbs</title>
  <aired>1986-02-26</aired>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if item.PremiereDate == nil || !item.PremiereDate.Equal(time.Date(1986, 2, 26, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("PremiereDate = %v, want 1986-02-26 (from <aired> fallback)", item.PremiereDate)
		}
	})

	t.Run("premiered takes priority over aired when both present", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<episodedetails>
  <title>Both Dates</title>
  <premiered>1986-04-02</premiered>
  <aired>1986-02-26</aired>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if item.PremiereDate == nil || !item.PremiereDate.Equal(time.Date(1986, 4, 2, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("PremiereDate = %v, want 1986-04-02 (premiered wins over aired)", item.PremiereDate)
		}
	})

	t.Run("genre tag populates Genres", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<episodedetails>
  <title>Genre Test</title>
  <genre>Anime</genre>
  <genre>Adventure</genre>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if len(item.Genres) != 2 || item.Genres[0] != "Anime" || item.Genres[1] != "Adventure" {
			t.Errorf("Genres = %v, want [Anime Adventure]", item.Genres)
		}
	})

	t.Run("uniqueid tmdb populates ProviderIDs", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<episodedetails>
  <title>Provider ID Test</title>
  <uniqueid type="tmdb">999</uniqueid>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if got := item.ProviderIDs["Tmdb"]; got != "999" {
			t.Errorf("ProviderIDs[Tmdb] = %q, want 999", got)
		}
	})

	t.Run("year, rating, mpaa, tags, originaltitle all flow through", func(t *testing.T) {
		dir := t.TempDir()
		video := writeEpisodeNFO(t, dir, `<episodedetails>
  <title>Full Fields</title>
  <originaltitle>Full Fields Original</originaltitle>
  <year>1986</year>
  <mpaa>TV-Y7</mpaa>
  <tag>saga</tag>
  <rating>8.2</rating>
</episodedetails>`)
		item := &storage.Item{}
		s.applyEpisodeNFO(pool, item, video)

		if item.OriginalTitle != "Full Fields Original" {
			t.Errorf("OriginalTitle = %q", item.OriginalTitle)
		}
		if item.ProductionYear == nil || *item.ProductionYear != 1986 {
			t.Errorf("ProductionYear = %v, want 1986", item.ProductionYear)
		}
		if item.OfficialRating != "TV-Y7" {
			t.Errorf("OfficialRating = %q, want TV-Y7", item.OfficialRating)
		}
		if len(item.Tags) != 1 || item.Tags[0] != "saga" {
			t.Errorf("Tags = %v, want [saga]", item.Tags)
		}
		if item.CommunityRating == nil || *item.CommunityRating != 8.2 {
			t.Errorf("CommunityRating = %v, want 8.2", item.CommunityRating)
		}
	})

	t.Run("no nfo file leaves item untouched", func(t *testing.T) {
		dir := t.TempDir()
		item := &storage.Item{Name: "Untouched"}
		s.applyEpisodeNFO(pool, item, filepath.Join(dir, "no-nfo-here.mkv"))
		if item.Name != "Untouched" {
			t.Errorf("Name = %q, want unchanged", item.Name)
		}
	})
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
