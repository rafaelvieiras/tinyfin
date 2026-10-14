package scan

import "testing"

func TestParseTitleYear(t *testing.T) {
	cases := []struct {
		name      string
		wantTitle string
		wantYear  int
	}{
		{"The Matrix (1999)", "The Matrix", 1999},
		{"Long Running Show (1973)", "Long Running Show", 1973},
		{"No Year Here", "No Year Here", 0},
		{"Weird (Not A Year)", "Weird (Not A Year)", 0},
	}
	for _, c := range cases {
		title, year := ParseTitleYear(c.name)
		if title != c.wantTitle || year != c.wantYear {
			t.Errorf("ParseTitleYear(%q) = (%q, %d), want (%q, %d)", c.name, title, year, c.wantTitle, c.wantYear)
		}
	}
}

func TestParseSeasonFolder(t *testing.T) {
	cases := []struct {
		name   string
		wantN  int
		wantOK bool
	}{
		{"Season 01", 1, true},
		{"season 1", 1, true},
		{"Season 00", 0, true},
		{"Season 12", 12, true},
		{"Specials", 0, true},
		{" season 3 ", 3, true},
		// Observed reference output for the season folder names below.
		{"Season 3 - Name", 3, true},
		{"Season 16 Redux", 16, true},
		{"Season 21 (2010)", 21, true},
		{"Season06", 6, true},
		{"Season_7", 7, true},
		{"Season.8", 8, true},
		{"Season-24", 24, true},
		{"Season  23", 23, true},
		{"Season 20000", 20000, true},
		{"Temporada 9", 9, true},
		{"Staffel 10", 10, true},
		{"Series 11", 11, true},
		{"S4", 4, true},
		{"S05", 5, true},
		{"S17 Complete", 17, true},
		{"12", 12, true},
		{"013", 13, true},
		{"Season Finale", 0, false},
		{"Season X", 0, false},
		{"Seasons 25", 0, false},
		{"Series", 0, false},
		{"Special", 0, false},
		{"S26E01", 0, false},
		{"Disc 1", 0, false},
		{"Part 3", 0, false},
		{"Extras", 0, false},
	}
	for _, c := range cases {
		n, ok := ParseSeasonFolder(c.name)
		if n != c.wantN || ok != c.wantOK {
			t.Errorf("ParseSeasonFolder(%q) = (%d, %v), want (%d, %v)", c.name, n, ok, c.wantN, c.wantOK)
		}
	}
}

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		filename   string
		wantSeason int
		wantEp     int
		wantTitle  string
		wantOK     bool
	}{
		{"Example Show - S01E02 - The Secret of the Example Orbs.mkv", 1, 2, "The Secret of the Example Orbs", true},
		{"Example Show - 1x02 - The Secret of the Example Orbs.mkv", 1, 2, "The Secret of the Example Orbs", true},
		{"S01E01.mkv", 1, 1, "", true},
		{"s1e1.mkv", 1, 1, "", true},
		{"Breaking Bad S05E14.mp4", 5, 14, "", true},
		{"just a movie file.mkv", 0, 0, "", false},
		{"MIS - Men in Suits - S01E10 - Alien Invasion.mkv", 1, 10, "Alien Invasion", true},
	}
	for _, c := range cases {
		s, e, title, ok := ParseEpisode(c.filename)
		if s != c.wantSeason || e != c.wantEp || title != c.wantTitle || ok != c.wantOK {
			t.Errorf("ParseEpisode(%q) = (%d, %d, %q, %v), want (%d, %d, %q, %v)",
				c.filename, s, e, title, ok, c.wantSeason, c.wantEp, c.wantTitle, c.wantOK)
		}
	}
}

func TestIsVideoFile(t *testing.T) {
	cases := map[string]bool{
		"movie.mkv": true, "movie.MP4": true, "poster.jpg": false, "readme.txt": false,
	}
	for name, want := range cases {
		if got := IsVideoFile(name); got != want {
			t.Errorf("IsVideoFile(%q) = %v, want %v", name, got, want)
		}
	}
}
