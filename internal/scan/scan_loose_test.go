package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// scanAll scans one library of the given type over root and returns every
// item keyed by its base file or folder name.
func scanAll(t *testing.T, root string, ct storage.CollectionType) (map[string]storage.Item, *Scanner, storage.Store) {
	t.Helper()
	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})
	lib := storage.Library{ID: "lib-1", Name: "Lib", CollectionType: ct, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if _, err := s.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &lib.ID, Limit: 1000})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	out := map[string]storage.Item{}
	for _, it := range page.Items {
		key := it.Name
		if it.Path != "" {
			key = filepath.Base(it.Path)
		}
		if it.Type == storage.ItemTypeSeason && it.Path == "" {
			key = "virtual:" + it.Name
		}
		out[key] = it
	}
	return out, s, st
}

func intVal(p *int) string {
	if p == nil {
		return "nil"
	}
	return string(rune('0' + *p%10))
}

func wantNum(t *testing.T, what string, got *int, want int) {
	t.Helper()
	if want < 0 {
		if got != nil {
			t.Errorf("%s = %d, want nil", what, *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %d", what, intVal(got), want)
	}
}

// Episode files that carry no SxxExx pattern are still episodes: parented
// under their folder's season (or a season without a folder), named after the
// file or the .nfo title, numbered only by what the name or the .nfo says.
func TestScanLooseEpisodes(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Spec Show (2000)")
	nfo := func(inner string) string {
		return "\xef\xbb\xbf<episodedetails>" + inner + "</episodedetails>"
	}
	files := map[string]string{
		"Season 00/Short One.mkv":                    nfo("<title>Short One</title><season>0</season><plot />"),
		"Season 00/Holiday Special - Story Name.mkv": nfo("<title>Holiday Special</title><season>0</season>"),
		"Season 00/S00E01 - Promo.mkv":               "",
		"Season 00/Zed NoNfo.mkv":                    "",
		"Season 00/Alpha WithEp.mkv":                 nfo("<title>Alpha With Ep</title><season>0</season><episode>5</episode>"),
		"Season 01/Spec Show S01E01.mkv":             "",
		"Season 01/Loose In Season.mkv":              "",
		"Season 01/Loose Conflict.mkv":               nfo("<title>Loose Conflict Title</title><season>3</season><episode>2</episode>"),
		"Season 01/Pattern Nfo S01E07.mkv":           nfo("<title>Pattern Nfo</title><episode>8</episode>"),
		"Root Loose.mkv":                             "",
		"Root Nfo.mkv":                               nfo("<title>Root Nfo Title</title><season>2</season><episode>4</episode>"),
		"Root Nfo NoSeason.mkv":                      nfo("<title>No Season</title><episode>4</episode>"),
		"Spec Show S01E09.mkv":                       "",
		"Specials/Special Folder.mkv":                "",
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(show, filepath.FromSlash(rel)), "x")
		if content != "" {
			writeFile(t, filepath.Join(show, filepath.FromSlash(rel[:len(rel)-len(filepath.Ext(rel))]+".nfo")), content)
		}
	}
	items, _, _ := scanAll(t, root, storage.CollectionTypeTVShows)

	type want struct {
		name     string
		sortName string
		season   int // ParentIndexNumber, -1 = nil
		episode  int // IndexNumber, -1 = nil
		parent   string
	}
	cases := map[string]want{
		"Short One.mkv":                    {"Short One", "000 - Short One", 0, -1, "Season 00"},
		"Holiday Special - Story Name.mkv": {"Holiday Special", "000 - Holiday Special", 0, -1, "Season 00"},
		"S00E01 - Promo.mkv":               {"S00E01 - Promo", "000 - 0001 - S00E01 - Promo", 0, 1, "Season 00"},
		"Zed NoNfo.mkv":                    {"Zed NoNfo", "Zed NoNfo", -1, -1, "Season 00"},
		"Alpha WithEp.mkv":                 {"Alpha With Ep", "000 - 0005 - Alpha With Ep", 0, 5, "Season 00"},
		"Spec Show S01E01.mkv":             {"Spec Show S01E01", "001 - 0001 - Spec Show S01E01", 1, 1, "Season 01"},
		"Loose In Season.mkv":              {"Loose In Season", "Loose In Season", -1, -1, "Season 01"},
		"Loose Conflict.mkv":               {"Loose Conflict Title", "003 - 0002 - Loose Conflict Title", 3, 2, "Season 01"},
		"Pattern Nfo S01E07.mkv":           {"Pattern Nfo", "001 - 0008 - Pattern Nfo", 1, 8, "Season 01"},
		"Root Loose.mkv":                   {"Root Loose", "Root Loose", -1, -1, "virtual:Season Unknown"},
		"Root Nfo.mkv":                     {"Root Nfo Title", "002 - 0004 - Root Nfo Title", 2, 4, "virtual:Season 2"},
		"Root Nfo NoSeason.mkv":            {"No Season", "0004 - No Season", -1, 4, "virtual:Season Unknown"},
		"Spec Show S01E09.mkv":             {"Spec Show S01E09", "001 - 0009 - Spec Show S01E09", 1, 9, "Season 01"},
		"Special Folder.mkv":               {"Special Folder", "Special Folder", -1, -1, "Specials"},
	}
	for file, w := range cases {
		it, ok := items[file]
		if !ok {
			t.Errorf("%s: not cataloged", file)
			continue
		}
		if it.Type != storage.ItemTypeEpisode {
			t.Errorf("%s: type = %s", file, it.Type)
		}
		if it.Name != w.name {
			t.Errorf("%s: Name = %q, want %q", file, it.Name, w.name)
		}
		if it.SortName != w.sortName {
			t.Errorf("%s: SortName = %q, want %q", file, it.SortName, w.sortName)
		}
		wantNum(t, file+" ParentIndexNumber", it.ParentIndexNumber, w.season)
		wantNum(t, file+" IndexNumber", it.IndexNumber, w.episode)
		parent, ok := items[w.parent]
		if !ok {
			t.Errorf("%s: season %q missing", file, w.parent)
			continue
		}
		if it.ParentID == nil || *it.ParentID != parent.ID || it.SeasonID == nil || *it.SeasonID != parent.ID {
			t.Errorf("%s: not parented under %q", file, w.parent)
		}
	}

	// Seasons: names, numbers, sort keys; none of them has a media file.
	seasons := map[string]struct {
		name, sort string
		index      int
	}{
		"Season 00":              {"Specials", "0000", 0},
		"Season 01":              {"Season 1", "0001", 1},
		"Specials":               {"Specials", "0000", 0},
		"virtual:Season Unknown": {"Season Unknown", "Season Unknown", -1},
		"virtual:Season 2":       {"Season 2", "0002", 2},
	}
	for key, w := range seasons {
		it, ok := items[key]
		if !ok {
			t.Errorf("season %q missing", key)
			continue
		}
		if it.Type != storage.ItemTypeSeason || it.Name != w.name || it.SortName != w.sort {
			t.Errorf("%s: got %s %q sort %q, want %q sort %q", key, it.Type, it.Name, it.SortName, w.name, w.sort)
		}
		wantNum(t, key+" IndexNumber", it.IndexNumber, w.index)
	}
}

// A season folder name tolerates surrounding and repeated white space.
func TestScanSeasonFolderWhitespace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, " Plain Series  (2012) ", "Season 1 ", "ep  S01E01  .mkv"), "x")
	items, _, _ := scanAll(t, root, storage.CollectionTypeTVShows)
	series := items[" Plain Series  (2012) "]
	if series.Name != "Plain Series" || series.ProductionYear == nil || *series.ProductionYear != 2012 || series.SortName != "plain series" {
		t.Errorf("series = %q year %v sort %q", series.Name, series.ProductionYear, series.SortName)
	}
	ep := items["ep  S01E01  .mkv"]
	if ep.ID == "" || ep.Name != "ep  S01E01  " {
		t.Fatalf("episode = %+v", ep)
	}
	if ep.SortName != "001 - 0001 - ep  S01E01  " {
		t.Errorf("episode SortName = %q (the file-derived title is kept as written)", ep.SortName)
	}
}

// Surrounding white space is dropped from every title taken from an .nfo and
// from the sort key; the file-derived name is left as it is.
func TestScanNFOTrimsSurroundingWhitespace(t *testing.T) {
	root := t.TempDir()
	movie := filepath.Join(root, "T Space (2001)")
	writeFile(t, filepath.Join(movie, "T Space (2001).mkv"), "x")
	writeFile(t, filepath.Join(movie, "T Space (2001).nfo"),
		"\xef\xbb\xbf<movie><title> \t Lead and trail \n</title><sorttitle>  The Sort 　</sorttitle>"+
			"<originaltitle> Orig </originaltitle><plot>\n\n Plot line 1\nline 2 \n</plot></movie>")
	zws := filepath.Join(root, "T Zws (2002)")
	writeFile(t, filepath.Join(zws, "T Zws (2002).mkv"), "x")
	writeFile(t, filepath.Join(zws, "T Zws (2002).nfo"), "<movie><title>​Zws​</title></movie>")
	blank := filepath.Join(root, "T Blank (2003)")
	writeFile(t, filepath.Join(blank, "T Blank (2003).mkv"), "x")
	writeFile(t, filepath.Join(blank, "T Blank (2003).nfo"), "<movie><title>   </title><plot> </plot></movie>")
	plain := filepath.Join(root, "Plain Lead  ")
	writeFile(t, filepath.Join(plain, " The Plain Lead .mkv"), "x")

	items, _, _ := scanAll(t, root, storage.CollectionTypeMovies)
	m := items["T Space (2001).mkv"]
	if m.Name != "Lead and trail" || m.SortName != "sort" || m.OriginalTitle != "Orig" || m.Overview != "Plot line 1\nline 2" {
		t.Errorf("trimmed movie = %q / %q / %q / %q", m.Name, m.SortName, m.OriginalTitle, m.Overview)
	}
	if z := items["T Zws (2002).mkv"]; z.Name != "​Zws​" {
		t.Errorf("zero width space was trimmed: %q", z.Name)
	}
	if b := items["T Blank (2003).mkv"]; b.Name != "T Blank" || b.Overview != "" {
		t.Errorf("blank title/plot: name %q overview %q", b.Name, b.Overview)
	}
	if p := items[" The Plain Lead .mkv"]; p.SortName != "plain lead" {
		t.Errorf("file-derived sort key = %q", p.SortName)
	}
}

func TestScanSeriesRuntimeFromNFO(t *testing.T) {
	cases := []struct {
		text string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"20", 12_000_000_000, true},
		{"-5", -3_000_000_000, true},
		{"+5", 3_000_000_000, true},
		{"020", 12_000_000_000, true},
		{"20 min", 12_000_000_000, true},
		{" 20 ", 0, false},
		{"12.5", 0, false},
		{"20.0", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"99999999999", 0, false},
	}
	for _, c := range cases {
		got, ok := nfoRuntimeTicks(c.text)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("nfoRuntimeTicks(%q) = %d, %v; want %d, %v", c.text, got, ok, c.want, c.ok)
		}
	}

	root := t.TempDir()
	zero := filepath.Join(root, "Run Zero")
	writeFile(t, filepath.Join(zero, "Season 01", "Run Zero S01E01.mkv"), "x")
	writeFile(t, filepath.Join(zero, "tvshow.nfo"), "<tvshow><title>Run Zero</title><runtime>0</runtime></tvshow>")
	none := filepath.Join(root, "Run None")
	writeFile(t, filepath.Join(none, "Season 01", "Run None S01E01.mkv"), "x")
	writeFile(t, filepath.Join(none, "tvshow.nfo"), "<tvshow><title>Run None</title></tvshow>")
	items, _, _ := scanAll(t, root, storage.CollectionTypeTVShows)
	if rt := items["Run Zero"].RunTimeTicks; rt == nil || *rt != 0 {
		t.Errorf("series with <runtime>0</runtime>: RunTimeTicks = %v, want 0", rt)
	}
	if rt := items["Run None"].RunTimeTicks; rt != nil {
		t.Errorf("series without <runtime>: RunTimeTicks = %v, want nil", *rt)
	}
}
