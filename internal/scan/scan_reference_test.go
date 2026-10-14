package scan

import (
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Movie names and years without .nfo, as observed on the reference
// (contract items.names.R9 to R12).
func TestScanMovieNamesWithoutNFO(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"Foo One (2015)/Foo One (2015).mkv",
		"Foo Two (2016)/Other Name.mkv",
		"Foo Three.2017.1080p.BluRay/Foo Three.2017.1080p.BluRay.mkv",
		"Foo Twelve (1899)/Foo Twelve (1899).mkv",
		"Foo Seven (2021).mkv",
		"Foo.Eight.2022.mkv",
		"Loose H 1080p.mkv",
		"Loose Q 2009 1080p 2010.mkv",
		"Multi (2010)/part1.mkv",
		"Multi (2010)/part2.mkv",
		"Stacked (2011)/Stacked (2011) - part1.mkv",
		"Stacked (2011)/Stacked (2011) - part2.mkv",
		"Versions (2014)/Versions (2014).mkv",
		"Versions (2014)/Versions (2014) - 4K.mkv",
		"Collection/Inner (2012)/Inner (2012).mkv",
		"Empty/readme.txt",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(f)), "x")
	}
	items, _, _ := scanAll(t, root, storage.CollectionTypeMovies)
	type want struct {
		name string
		year int
	}
	cases := map[string]want{
		"Foo One (2015).mkv":              {"Foo One (2015)", 2015},
		"Other Name.mkv":                  {"Foo Two (2016)", 2016},
		"Foo Three.2017.1080p.BluRay.mkv": {"Foo Three.2017.1080p.BluRay", 2017},
		"Foo Twelve (1899).mkv":           {"Foo Twelve (1899)", 0},
		"Foo Seven (2021).mkv":            {"Foo Seven", 2021},
		"Foo.Eight.2022.mkv":              {"Foo.Eight", 2022},
		"Loose H 1080p.mkv":               {"Loose H", 0},
		"Loose Q 2009 1080p 2010.mkv":     {"Loose Q 2009", 2010},
		"part1.mkv":                       {"part1", 0},
		"part2.mkv":                       {"part2", 0},
		"Stacked (2011) - part1.mkv":      {"Stacked (2011)", 2011},
		"Versions (2014).mkv":             {"Versions (2014)", 2014},
		"Inner (2012).mkv":                {"Inner (2012)", 2012},
	}
	for file, w := range cases {
		it, ok := items[file]
		if !ok {
			t.Errorf("%s: not cataloged", file)
			continue
		}
		if it.Type != storage.ItemTypeMovie || it.Name != w.name {
			t.Errorf("%s: %s %q, want Movie %q", file, it.Type, it.Name, w.name)
		}
		got := 0
		if it.ProductionYear != nil {
			got = *it.ProductionYear
		}
		if got != w.year {
			t.Errorf("%s: year %d, want %d", file, got, w.year)
		}
	}
	if _, ok := items["Stacked (2011) - part2.mkv"]; ok {
		t.Error("the second part of a stack is an item of its own")
	}
	if _, ok := items["Versions (2014) - 4K.mkv"]; ok {
		t.Error("a version of a movie is an item of its own")
	}
	for _, d := range []string{"Multi (2010)", "Collection", "Empty"} {
		if it, ok := items[d]; !ok || it.Type != storage.ItemTypeFolder {
			t.Errorf("%s: want a Folder item, got %+v", d, it.Type)
		}
	}
	inner, folder := items["Inner (2012).mkv"], items["Collection"]
	if inner.ParentID == nil || *inner.ParentID != folder.ID {
		t.Error("a movie below a folder is parented under it")
	}
	if p := items["Multi (2010)"]; items["part1.mkv"].ParentID == nil || *items["part1.mkv"].ParentID != p.ID {
		t.Error("movies of a multi-file folder are parented under the folder")
	}
}

// Folders of a series that are not season folders are seasons named after
// them; extras folders are left out; episodes naming a season that has no
// folder get an empty season.
func TestScanSeriesFoldersAsSeasons(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Dirs Show (2003)")
	for _, f := range []string{
		"Season 01/Dirs Show S01E01.mkv",
		"Season 01/Disc A/Dirs Show S01E02.mkv",
		"Season 1 Bonus/Bonus S01E05.mkv",
		"Part 2/Dirs Show S02E01.mkv",
		"Season 7 Redux/Dirs Show S07E02.mkv",
		"Extras/Extra One.mkv",
		"Behind The Scenes/BTS.mkv",
		"Loose Root.mkv",
	} {
		writeFile(t, filepath.Join(show, filepath.FromSlash(f)), "x")
	}
	items, _, _ := scanAll(t, root, storage.CollectionTypeTVShows)
	byPath := func(rel string) storage.Item { return items[filepath.Base(rel)] }
	s1, part2, s7 := byPath("Season 01"), byPath("Part 2"), byPath("Season 7 Redux")
	if s1.Name != "Season 1" || s1.IndexNumber == nil || *s1.IndexNumber != 1 {
		t.Errorf("Season 01 = %q %v", s1.Name, s1.IndexNumber)
	}
	if part2.Type != storage.ItemTypeSeason || part2.Name != "Part 2" || part2.IndexNumber != nil {
		t.Errorf("Part 2 = %s %q %v", part2.Type, part2.Name, part2.IndexNumber)
	}
	if s7.Name != "Season 7" || s7.IndexNumber == nil || *s7.IndexNumber != 7 {
		t.Errorf("Season 7 Redux = %q %v", s7.Name, s7.IndexNumber)
	}
	if _, ok := items["Season 1 Bonus"]; ok {
		t.Error("a second folder of season 1 is a season of its own")
	}
	bonus := items["Bonus S01E05.mkv"]
	if bonus.SeasonID == nil || *bonus.SeasonID != s1.ID {
		t.Error("an episode of Season 1 Bonus belongs to season 1")
	}
	for _, name := range []string{"Extras", "Behind The Scenes", "Extra One.mkv", "BTS.mkv"} {
		if _, ok := items[name]; ok {
			t.Errorf("%s must be left out", name)
		}
	}
	disc := items["Disc A"]
	if disc.Type != storage.ItemTypeFolder || disc.ParentID == nil || *disc.ParentID != s1.ID {
		t.Errorf("Disc A = %s, want a Folder inside season 1", disc.Type)
	}
	ep := items["Dirs Show S01E02.mkv"]
	if ep.ParentID == nil || *ep.ParentID != disc.ID || ep.SeasonID == nil || *ep.SeasonID != s1.ID {
		t.Error("an episode in a folder of a season: parent is the folder, season the season")
	}
	// S02E01 in "Part 2": the empty season 2 exists.
	var s2 *storage.Item
	for _, it := range items {
		if it.Type == storage.ItemTypeSeason && it.IndexNumber != nil && *it.IndexNumber == 2 {
			it := it
			s2 = &it
		}
	}
	if s2 == nil || s2.Path != "" {
		t.Errorf("season 2 (no folder) missing: %+v", s2)
	}
	// A file with no number goes to the first season without a number.
	loose := items["Loose Root.mkv"]
	if loose.SeasonID == nil || *loose.SeasonID != part2.ID {
		t.Error("a loose file joins the first unnumbered season")
	}
}

// Pictures of a home-videos library are photos; a folder that directly holds
// one is a photo album; folder artwork and the images of a video are not
// photos.
func TestScanHomeVideosPhotos(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"Album/pic1.jpg", "Album/Clip.mkv", "Album/Clip.jpg", "Album/poster.png", "Album/banner.jpg",
		"Videos/Only.mkv", "root.PNG",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(f)), "x")
	}
	items, _, _ := scanAll(t, root, storage.CollectionTypeHomeVideos)
	if a := items["Album"]; a.Type != storage.ItemTypePhotoAlbum {
		t.Errorf("Album = %s", a.Type)
	}
	if v := items["Videos"]; v.Type != storage.ItemTypeFolder {
		t.Errorf("Videos = %s", v.Type)
	}
	for _, f := range []string{"pic1.jpg", "banner.jpg", "root.PNG"} {
		if it := items[f]; it.Type != storage.ItemTypePhoto || it.Name != f[:len(f)-4] {
			t.Errorf("%s = %s %q", f, it.Type, it.Name)
		}
	}
	for _, f := range []string{"Clip.jpg", "poster.png"} {
		if _, ok := items[f]; ok {
			t.Errorf("%s must not be a photo", f)
		}
	}
	if p := items["pic1.jpg"]; p.Album != "Album" {
		t.Errorf("photo album = %q", p.Album)
	}
}

// A file holding several <episodedetails> is one episode named after all of
// them.
func TestScanMultiDocumentEpisodeNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Multi Show (2004)", "Season 01")
	writeFile(t, filepath.Join(dir, "Multi S01E01E02.mkv"), "x")
	writeFile(t, filepath.Join(dir, "Multi S01E01E02.nfo"),
		"<episodedetails><title>Second</title><season>1</season><episode>2</episode></episodedetails>\n"+
			"<episodedetails><title>First</title><season>1</season><episode>1</episode></episodedetails>\n")
	items, _, _ := scanAll(t, root, storage.CollectionTypeTVShows)
	ep := items["Multi S01E01E02.mkv"]
	if ep.Name != "First / Second" || ep.IndexNumber == nil || *ep.IndexNumber != 1 || ep.IndexNumberEnd == nil || *ep.IndexNumberEnd != 2 {
		t.Errorf("episode = %q %v-%v", ep.Name, ep.IndexNumber, ep.IndexNumberEnd)
	}
}
