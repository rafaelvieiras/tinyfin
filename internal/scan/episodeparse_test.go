package scan

import (
	"testing"
	"time"
)

func ip(n int) *int { return &n }

// TestParseEpisodeFileReference replays file names probed on the 12.2
// reference (series folder "Bat", no season folder, no .nfo). The season is
// the one the name carries; a season folder fills it in separately.
func TestParseEpisodeFileReference(t *testing.T) {
	cases := []struct {
		file    string
		name    string
		season  *int
		episode *int
		end     *int
		year    int
		date    string
	}{
		{"Bat - 05.mp4", "Bat", nil, ip(5), nil, 0, ""},
		{"Bat - 5.mp4", "Bat", nil, ip(5), nil, 0, ""},
		{"Bat - 105.mp4", "Bat", nil, ip(105), nil, 0, ""},
		{"Bat -05.mp4", "Bat -05", nil, nil, nil, 0, ""},
		{"Bat 05.mp4", "Bat 05", nil, ip(5), nil, 0, ""},
		{"Bat 5.mp4", "Bat 5", nil, ip(5), nil, 0, ""},
		{"Bat 12 Title.mp4", "Bat 12 Title", nil, ip(12), nil, 0, ""},
		{"Bat - 05 - Title.mp4", "Bat - 05 - Title", nil, ip(5), nil, 0, ""},
		{"Bat .05.mp4", "Bat .05", nil, nil, nil, 0, ""},
		{"Bat _05.mp4", "Bat _05", nil, nil, nil, 0, ""},
		{"Bat .05.Title.mp4", "Bat .05.Title", nil, nil, nil, 0, ""},
		{"Bat 2020.mp4", "Bat", nil, ip(2020), nil, 2020, ""},
		{"Bat 1999.mp4", "Bat", nil, ip(1999), nil, 1999, ""},
		{"Bat 2200.mp4", "Bat 2200", nil, ip(2200), nil, 0, ""},
		{"Bat 209.mp4", "Bat 209", nil, ip(209), nil, 0, ""},
		{"Bat 2209.mp4", "Bat 2209", nil, ip(2209), nil, 0, ""},
		{"Bat 3x09.mp4", "Bat 3x09", ip(3), ip(9), nil, 0, ""},
		{"Bat 03x09.mp4", "Bat 03x09", ip(3), ip(9), nil, 0, ""},
		{"Bat 1x2x3.mp4", "Bat 1x2x3", ip(1), ip(2), ip(3), 0, ""},
		{"Bat E05.mp4", "Bat E05", nil, ip(5), nil, 0, ""},
		{"Bat e05.mp4", "Bat e05", nil, ip(5), nil, 0, ""},
		{"Bat Ep05.mp4", "Bat Ep05", nil, ip(5), nil, 0, ""},
		{"Bat Episode 5.mp4", "Bat Episode 5", nil, ip(5), nil, 0, ""},
		{"Bat Ep. 5.mp4", "Bat Ep. 5", nil, nil, nil, 0, ""},
		{"Bat Ep 5 Title.mp4", "Bat Ep 5 Title", nil, ip(5), nil, 0, ""},
		{"Bat S01.mp4", "Bat S01", nil, nil, nil, 0, ""},
		{"Bat S01E05E06.mp4", "Bat S01E05E06", ip(1), ip(5), ip(6), 0, ""},
		{"Bat S01E05-E06.mp4", "Bat S01E05-E06", ip(1), ip(5), ip(6), 0, ""},
		{"Bat S01E05-06.mp4", "Bat S01E05-06", ip(1), ip(5), ip(6), 0, ""},
		{"Bat S01E05 E06.mp4", "Bat S01E05 E06", ip(1), ip(5), nil, 0, ""},
		{"Bat 1x05-06.mp4", "Bat 1x05-06", ip(1), ip(5), ip(6), 0, ""},
		{"Bat Season 1 Episode 2.mp4", "Bat Season 1 Episode 2", ip(1), ip(2), nil, 0, ""},
		{"Bat Season 1.mp4", "Bat Season 1", nil, ip(1), nil, 0, ""},
		{"Bat Season1 Episode2.mp4", "Bat Season1 Episode2", nil, ip(2), nil, 0, ""},
		{"Bat S1 E2.mp4", "Bat S1 E2", ip(1), ip(2), nil, 0, ""},
		{"Bat S1.E2.mp4", "Bat S1.E2", ip(1), ip(2), nil, 0, ""},
		{"Bat S01 E02.mp4", "Bat S01 E02", ip(1), ip(2), nil, 0, ""},
		{"Bat 2020-01-31.mp4", "Bat 2020-01-31", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat 2020.01.31.mp4", "Bat 2020.01.31", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat 31-01-2020.mp4", "Bat 31-01", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat 01-31-2020.mp4", "Bat 01-31", nil, nil, nil, 2020, ""},
		{"Bat 2020 01 31.mp4", "Bat 2020 01 31", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat 20200131.mp4", "Bat 20200131", nil, ip(2020), nil, 0, ""},
		{"Bat 200131.mp4", "Bat 200131", nil, ip(2001), nil, 0, ""},
		{"Bat 2020-01-31 Title.mp4", "Bat 2020-01-31 Title", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat Title 2020-01-31.mp4", "Bat Title 2020-01-31", nil, nil, nil, 2020, "2020-01-31"},
		{"Bat [01].mp4", "Bat", nil, ip(1), nil, 0, ""},
		{"Bat (05).mp4", "Bat (05)", nil, nil, nil, 0, ""},
		{"Bat [S01E05].mp4", "Bat", ip(1), ip(5), nil, 0, ""},
		{"Bat [1x05].mp4", "Bat", ip(1), ip(5), nil, 0, ""},
		{"Bat - S01E05 - Title.mp4", "Bat - S01E05 - Title", ip(1), ip(5), nil, 0, ""},
		{"Bat ep01.mp4", "Bat ep01", nil, ip(1), nil, 0, ""},
		{"Bat 01.mp4", "Bat 01", nil, ip(1), nil, 0, ""},
		{"Bat 001.mp4", "Bat 001", nil, ip(1), nil, 0, ""},
		{"Bat 0001.mp4", "Bat 0001", nil, ip(1), nil, 0, ""},
		{"Bat #05.mp4", "Bat #05", nil, nil, nil, 0, ""},
		{"Bat Part 2.mp4", "Bat Part 2", nil, ip(2), nil, 0, ""},
		{"Bat Pt 2.mp4", "Bat Pt 2", nil, ip(2), nil, 0, ""},
		{"Bat Special.mp4", "Bat Special", nil, nil, nil, 0, ""},
		{"Bat 1080p.mp4", "Bat", nil, ip(1080), nil, 0, ""},
		{"Bat 720p 05.mp4", "Bat", nil, ip(720), nil, 0, ""},
		{"Bat x264 05.mp4", "Bat", nil, ip(5), nil, 0, ""},
		{"Bat 05 720p.mp4", "Bat 05", nil, ip(5), nil, 0, ""},
		{"Bat 05v2.mp4", "Bat 05v2", nil, ip(5), nil, 0, ""},
		{"Bat S01E02.mp4", "Bat S01E02", ip(1), ip(2), nil, 0, ""},
		{"Bat S01E02 Title Here.mp4", "Bat S01E02 Title Here", ip(1), ip(2), nil, 0, ""},
		{"Bat Title S01E02.mp4", "Bat Title S01E02", ip(1), ip(2), nil, 0, ""},
		{"Bat S01E02.E03.mp4", "Bat S01E02.E03", ip(1), ip(2), nil, 0, ""},
		{"Bat S01E02E03E04.mp4", "Bat S01E02E03E04", ip(1), ip(2), ip(4), 0, ""},
		{"Bat s01e02.mp4", "Bat s01e02", ip(1), ip(2), nil, 0, ""},
		{"Bat S01.E02.mp4", "Bat S01.E02", ip(1), ip(2), nil, 0, ""},
		{"Bat S01 - E02.mp4", "Bat S01 - E02", ip(1), ip(2), nil, 0, ""},
		{"Bat Season 01 Episode 02.mp4", "Bat Season 01 Episode 02", ip(1), ip(2), nil, 0, ""},
		{"Bat 1x02.mp4", "Bat 1x02", ip(1), ip(2), nil, 0, ""},
		{"Bat S2005E10.mp4", "Bat S2005E10", ip(2005), ip(10), nil, 0, ""},
		{"Bat 2x1080p.mp4", "Bat 2x1080p", ip(2), ip(1080), nil, 0, ""},
		{"Bat Ep.5.mp4", "Bat Ep.5", nil, nil, nil, 0, ""},
		{"Bat Ep. 05.mp4", "Bat Ep. 05", nil, nil, nil, 0, ""},
		{"Bat Vol. 5.mp4", "Bat Vol. 5", nil, nil, nil, 0, ""},
		{"Bat No. 5.mp4", "Bat No. 5", nil, nil, nil, 0, ""},
		{"Bat Dr. 5.mp4", "Bat Dr. 5", nil, nil, nil, 0, ""},
		{"Bat A. 5.mp4", "Bat A. 5", nil, nil, nil, 0, ""},
		{"Bat 5..mp4", "Bat 5.", nil, ip(5), nil, 0, ""},
		{"Bat 5. Title.mp4", "Bat 5. Title", nil, ip(5), nil, 0, ""},
		{"Bat Title. 5.mp4", "Bat Title. 5", nil, nil, nil, 0, ""},
		{"Bat v5.mp4", "Bat v5", nil, nil, nil, 0, ""},
		{"Bat Part5.mp4", "Bat Part5", nil, nil, nil, 0, ""},
		{"Bat Part.5.mp4", "Bat Part.5", nil, ip(5), nil, 0, ""},
		{"Bat Pt. 2.mp4", "Bat Pt. 2", nil, nil, nil, 0, ""},
		{"Bat Episode.5.mp4", "Bat Episode.5", nil, nil, nil, 0, ""},
		{"Bat Episode05.mp4", "Bat Episode05", nil, ip(5), nil, 0, ""},
		{"Bat Disc 1.mp4", "Bat Disc 1", nil, ip(1), nil, 0, ""},
		{"Bat CD1.mp4", "Bat", nil, nil, nil, 0, ""},
		{"Bat 3 Title 5.mp4", "Bat 3 Title 5", nil, ip(3), nil, 0, ""},
		{"Bat 100.mp4", "Bat 100", nil, ip(100), nil, 0, ""},
		{"Bat 99999.mp4", "Bat 99999", nil, ip(9999), nil, 0, ""},
		{"Bat 12345.mp4", "Bat 12345", nil, ip(1234), nil, 0, ""},
		{"Bat 5 6.mp4", "Bat 5 6", nil, ip(5), nil, 0, ""},
		{"Bat 5-6.mp4", "Bat 5-6", nil, ip(5), nil, 0, ""},
		{"Bat 05 - 06.mp4", "Bat 05", nil, ip(5), nil, 0, ""},
		{"Bat 5of10.mp4", "Bat 5of10", nil, ip(5), nil, 0, ""},
		{"Bat 5 of 10.mp4", "Bat 5 of 10", nil, ip(5), nil, 0, ""},
		{"Bat 1999 Special.mp4", "Bat", nil, ip(1999), nil, 1999, ""},
		{"Bat 2021 Title 3.mp4", "Bat", nil, ip(2021), nil, 2021, ""},
		{"Bat Title 3.mp4", "Bat Title 3", nil, ip(3), nil, 0, ""},
		{"Bat 3Title.mp4", "Bat 3Title", nil, ip(3), nil, 0, ""},
		{"Bat Title 3 Subtitle.mp4", "Bat Title 3 Subtitle", nil, ip(3), nil, 0, ""},
		{"Bat 10.5.mp4", "Bat 10.5", nil, ip(10), nil, 0, ""},
		{"Bat 2-1.mp4", "Bat 2-1", nil, ip(2), nil, 0, ""},
		{"Bat 1-2-3.mp4", "Bat 1-2-3", nil, ip(1), nil, 0, ""},
		{"Bat S01E02 2020.mp4", "Bat S01E02", ip(1), ip(2), nil, 2020, ""},
		{"Bat Title 1080p.mp4", "Bat Title", nil, ip(1080), nil, 0, ""},
		{"Bat - 1080p.mp4", "Bat -", nil, ip(108), nil, 0, ""},
		{"Bat - 05 720p.mp4", "Bat", nil, ip(5), nil, 0, ""},
		{"Bat - Title.mp4", "Bat - Title", nil, nil, nil, 0, ""},
		{"Bat 3 - Title.mp4", "Bat 3 - Title", nil, ip(3), nil, 0, ""},
		{"Bat 03. Title.mp4", "Bat 03. Title", nil, ip(3), nil, 0, ""},
		{"Bat 03.Title.mp4", "Bat 03.Title", nil, ip(3), nil, 0, ""},
		{"Bat (2019) 05.mp4", "Bat", nil, nil, nil, 2019, ""},
		{"Bat 05 (2019).mp4", "Bat 05", nil, ip(5), nil, 2019, ""},
		{"Bat 1 2 3.mp4", "Bat 1 2 3", nil, ip(1), nil, 0, ""},
		{"Bat E5 Title.mp4", "Bat E5 Title", nil, nil, nil, 0, ""},
		{"Bat E05.Title.mp4", "Bat E05.Title", nil, ip(5), nil, 0, ""},
		{"Bat Episode 5 - Title.mp4", "Bat Episode 5 - Title", nil, ip(5), nil, 0, ""},
		{"Bat EP5.mp4", "Bat EP5", nil, ip(5), nil, 0, ""},
		{"Bat Ep 05 - 06.mp4", "Bat Ep 05", nil, ip(5), nil, 0, ""},
		{"Bat 2x05-2x06.mp4", "Bat 2x05-2x06", ip(2), ip(5), ip(6), 0, ""},
		{"Bat Special 1.mp4", "Bat Special 1", nil, ip(1), nil, 0, ""},
		{"Bat Special 01.mp4", "Bat Special 01", nil, ip(1), nil, 0, ""},
		{"Bat S00E01.mp4", "Bat S00E01", ip(0), ip(1), nil, 0, ""},
		{"Bat 01 of 12.mp4", "Bat 01 of 12", nil, ip(1), nil, 0, ""},
		{"Bat Title - 01 - Name.mp4", "Bat Title - 01 - Name", nil, ip(1), nil, 0, ""},
		{"Bat Title - 01.mp4", "Bat Title", nil, ip(1), nil, 0, ""},
		{"Bat Title 01 -.mp4", "Bat Title 01 -", nil, ip(1), nil, 0, ""},
		{"Bat Title-01.mp4", "Bat Title-01", nil, nil, nil, 0, ""},
		{"Bat [Group] 05.mp4", "Bat [Group] 05", nil, nil, nil, 0, ""},
		{"Bat [Group] - 05 [720p].mp4", "Bat [Group]", nil, ip(5), nil, 0, ""},
		{"Bat 05 [Group].mp4", "Bat 05", nil, ip(5), nil, 0, ""},
		{"Bat Name_05.mp4", "Bat Name_05", nil, nil, nil, 0, ""},
		{"Bat Name.05.mp4", "Bat Name.05", nil, nil, nil, 0, ""},
		{"Bat (05).mp4", "Bat (05)", nil, nil, nil, 0, ""},
		{"Bat A 5.mp4", "Bat A 5", nil, ip(5), nil, 0, ""},
	}
	eq := func(a, b *int) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	fmtp := func(p *int) any {
		if p == nil {
			return nil
		}
		return *p
	}
	for _, c := range cases {
		got := ParseEpisodeFile(c.file)
		date := ""
		if got.Date != nil {
			date = got.Date.Format(time.DateOnly)
		}
		if got.Name != c.name || !eq(got.Season, c.season) || !eq(got.Episode, c.episode) || !eq(got.EpisodeEnd, c.end) || got.Year != c.year || date != c.date {
			t.Errorf("ParseEpisodeFile(%q) = name %q season %v episode %v end %v year %d date %q; want name %q season %v episode %v end %v year %d date %q",
				c.file, got.Name, fmtp(got.Season), fmtp(got.Episode), fmtp(got.EpisodeEnd), got.Year, date,
				c.name, fmtp(c.season), fmtp(c.episode), fmtp(c.end), c.year, c.date)
		}
	}
}
