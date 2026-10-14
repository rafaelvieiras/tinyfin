package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func touch(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(f)), "x")
	}
}

func imagePaths(t *testing.T, st storage.Store, id storage.ItemID, kind string) []string {
	t.Helper()
	refs, err := st.GetImageRefs(context.Background(), id, storage.ImageKind(kind))
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	var out []string
	for _, r := range refs {
		out = append(out, filepath.Base(r.Path))
	}
	return out
}

func extrasOf(t *testing.T, st storage.Store, owner storage.ItemID, types ...string) []storage.Item {
	t.Helper()
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{OwnerID: &owner, ExtraTypes: types, Limit: 500})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	return page.Items
}

func itemNames(items []storage.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name+"/"+it.ExtraType)
	}
	return out
}

func TestLocalImageNamesAndExtensions(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Prim (2201)/Prim (2201).mkv", "Prim (2201)/poster.jpg", "Prim (2201)/poster.PNG", "Prim (2201)/folder.jpg",
		"Prim (2201)/Prim (2201)-poster.webp", "Prim (2201)/clearlogo.png", "Prim (2201)/logo.avif",
		"Prim (2201)/landscape.tbn", "Prim (2201)/thumb.jpg", "Prim (2201)/cdart.jpg", "Prim (2201)/discart.jpg",
		"Prim (2201)/box.jpg", "Prim (2201)/screenshot.jpg",
		"Bd (2202)/Bd (2202).mkv", "Bd (2202)/fanart.jpg", "Bd (2202)/fanart-2.jpg", "Bd (2202)/backdrop1.jpg",
		"Bd (2202)/backdrop3.jpg", "Bd (2202)/backdrop50.jpg", "Bd (2202)/fanart1.jpg", "Bd (2202)/backdrop-1.jpg",
		"Bd (2202)/extrafanart/b.jpg", "Bd (2202)/extrafanart/a.png",
		"Dup (2203)/Dup (2203).mkv", "Dup (2203)/Dup (2203)-fanart.jpg",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeMovies)
	prim := items["Prim (2201).mkv"]
	if got := imagePaths(t, st, prim.ID, "Primary"); len(got) != 1 || got[0] != "Prim (2201)-poster.webp" {
		t.Errorf("Primary = %v, want the video's own -poster", got)
	}
	for kind, want := range map[string]string{"Logo": "clearlogo.png", "Thumb": "landscape.tbn", "Disc": "cdart.jpg"} {
		if got := imagePaths(t, st, prim.ID, kind); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want %s", kind, got, want)
		}
	}
	for _, kind := range []string{"Box", "Screenshot", "Banner"} {
		if got := imagePaths(t, st, prim.ID, kind); len(got) != 0 {
			t.Errorf("%s = %v, want none", kind, got)
		}
	}
	bd := items["Bd (2202).mkv"]
	got := strings.Join(imagePaths(t, st, bd.ID, "Backdrop"), ",")
	// fanart, fanart-2, extrafanart/*, backdrop1, backdrop3: not fanart1,
	// backdrop-1 or backdrop50 (two numbers in a row are missing before it).
	if got != "fanart.jpg,fanart-2.jpg,a.png,b.jpg,backdrop1.jpg,backdrop3.jpg" {
		t.Errorf("Backdrop = %s", got)
	}
	dup := items["Dup (2203).mkv"]
	if got := imagePaths(t, st, dup.ID, "Backdrop"); len(got) != 2 {
		t.Errorf("a lone <video>-fanart is two backdrops, got %v", got)
	}
}

func TestLocalImagesSharedFolderAndSeasons(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Pair/One (2001).mkv", "Pair/Two (2001).mkv", "Pair/poster.jpg", "Pair/One (2001)-poster.jpg",
		"Pair/Two (2001)-fanart.jpg", "Pair/fanart.jpg",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeMovies)
	if got := imagePaths(t, st, items["One (2001).mkv"].ID, "Primary"); len(got) != 1 || got[0] != "One (2001)-poster.jpg" {
		t.Errorf("One Primary = %v", got)
	}
	if got := imagePaths(t, st, items["Two (2001).mkv"].ID, "Backdrop"); len(got) != 1 {
		t.Errorf("a shared-folder movie reads <video>-fanart once, got %v", got)
	}
	folder := items["Pair"]
	if got := imagePaths(t, st, folder.ID, "Primary"); len(got) != 1 || got[0] != "poster.jpg" {
		t.Errorf("folder Primary = %v", got)
	}

	root = t.TempDir()
	touch(t, root,
		"Show (2020)/Season 01/Show S01E01.mkv", "Show (2020)/Season 02/Show S02E01.mkv", "Show (2020)/Specials/Show S00E01.mkv",
		"Show (2020)/poster.jpg", "Show (2020)/season1-poster.png", "Show (2020)/season01-poster.jpg",
		"Show (2020)/season01-banner.jpg", "Show (2020)/season01.jpg", "Show (2020)/season-specials-poster.jpg",
		"Show (2020)/Season 01/landscape.jpg", "Show (2020)/Season 02/folder.jpg",
		"Show (2020)/Season 01/Show S01E01-thumb.jpg", "Show (2020)/Season 02/metadata/Show S02E01.jpg",
	)
	items, _, st = scanAll(t, root, storage.CollectionTypeTVShows)
	check := func(file, kind, want string) {
		t.Helper()
		got := imagePaths(t, st, items[file].ID, kind)
		if want == "" && len(got) == 0 {
			return
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s %s = %v, want %q", file, kind, got, want)
		}
	}
	check("Season 01", "Primary", "season1-poster.png")
	check("Season 01", "Banner", "season01-banner.jpg")
	check("Season 01", "Thumb", "landscape.jpg")
	check("Season 02", "Primary", "folder.jpg")
	check("Specials", "Primary", "season-specials-poster.jpg")
	check("Show S01E01.mkv", "Primary", "Show S01E01-thumb.jpg")
	check("Show S02E01.mkv", "Primary", "Show S02E01.jpg")
	check("Show (2020)", "Primary", "poster.jpg")
}

func TestExtrasAreCatalogedAsOwnedItems(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Ex (2001)/Ex (2001).mkv",
		"Ex (2001)/Ex (2001)-trailer.mkv", "Ex (2001)/trailer.mkv", "Ex (2001)/x-trailer.mkv",
		"Ex (2001)/trailers/Folder Trailer.mkv", "Ex (2001)/extras/Making Of.mkv", "Ex (2001)/featurettes/Deep.mkv",
		"Ex (2001)/Ex (2001)-behindthescenes.mkv", "Ex (2001)/Ex (2001).featurette.mkv",
		"Ex (2001)/theme.mp3", "Ex (2001)/theme-music/b.flac",
		"Ex (2001)/backdrops/Loop.mkv", "Ex (2001)/soundtrack.mp3",
		"Plain (2002)/Plain (2002).mkv",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeMovies)
	var owner storage.Item
	for _, it := range items {
		if it.Path != "" && filepath.Base(it.Path) == "Ex (2001).mkv" {
			owner = it
		}
	}
	if owner.ID == "" {
		t.Fatal("movie not cataloged")
	}
	got := strings.Join(itemNames(extrasOf(t, st, owner.ID)), ",")
	want := "Behind The Scenes/BehindTheScenes,Deep/Featurette,Folder/Trailer,Loop/ThemeVideo,Making Of/Unknown,Theme Song/ThemeSong,Trailer/Trailer,Trailer 2/Trailer,b/ThemeSong,x/Trailer,featurette/Version"
	// order is by name in the API; the store returns insertion order, so
	// compare as sets.
	if !sameSet(strings.Split(got, ","), strings.Split(want, ",")) {
		t.Errorf("extras = %s\nwant    %s", got, want)
	}
	// extras are in no listing
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &owner.LibraryID, Recursive: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.ExtraType != "" {
			t.Errorf("extra %q listed", it.Name)
		}
	}
	// no audio item is cataloged in a movie library
	for _, it := range page.Items {
		if it.Type == storage.ItemTypeAudio {
			t.Errorf("audio item %q in a movie library", it.Name)
		}
	}
}

// A movie whose folder holds a sub folder is a Folder item: it keeps what its
// extras folders hold, and loses all of it when a trailer file sits beside it.
func TestExtrasOfAMovieInAFolderItem(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Keep (2003)/Keep (2003).mkv", "Keep (2003)/soundtrack/01.mp3", "Keep (2003)/trailers/T.mkv", "Keep (2003)/theme.mp3",
		"Lose (2004)/Lose (2004).mkv", "Lose (2004)/soundtrack/01.mp3", "Lose (2004)/Lose (2004)-trailer.mkv", "Lose (2004)/theme.mp3",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeMovies)
	if n := len(extrasOf(t, st, items["Keep (2003).mkv"].ID)); n != 2 {
		t.Errorf("Keep extras = %d, want 2 (trailer, theme)", n)
	}
	if got := extrasOf(t, st, items["Lose (2004).mkv"].ID); len(got) != 0 {
		t.Errorf("Lose extras = %v, want none", itemNames(got))
	}
	if f := items["Keep (2003)"]; f.Type != storage.ItemTypeFolder {
		t.Errorf("Keep (2003) = %v, want a Folder item", f.Type)
	}
	if m := items["Keep (2003).mkv"]; m.ParentID == nil || *m.ParentID != items["Keep (2003)"].ID {
		t.Errorf("the movie's parent is not its folder item")
	}
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestStackedPartsAndVersions(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Stack (2001)/Stack (2001) - part1.mkv", "Stack (2001)/Stack (2001) - part2.mkv", "Stack (2001)/Stack (2001) - part3.mkv",
		"Ver (2002)/Ver (2002).mkv", "Ver (2002)/Ver (2002) - 4K.mkv", "Ver (2002)/Ver (2002).3D.mkv",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeMovies)
	stack := items["Stack (2001) - part1.mkv"]
	parts := extrasOf(t, st, stack.ID, storage.ExtraTypePart)
	if len(parts) != 2 {
		t.Errorf("parts = %v, want 2", itemNames(parts))
	}
	ver := items["Ver (2002).mkv"]
	vs := extrasOf(t, st, ver.ID, storage.ExtraTypeVersion)
	got := strings.Join(itemNames(vs), ",")
	if !sameSet(strings.Split(got, ","), []string{"4K/Version", "3D/Version"}) {
		t.Errorf("versions = %s", got)
	}
}

func TestEpisodeVersionsMergedSeasonsAndEpisodeFolders(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Ser (2020)/Season 01/Ser S01E01.mkv", "Ser (2020)/Season 01/Ser S01E01 1080p.mkv", "Ser (2020)/Season 01/Ser S01E01 - 4K.mkv",
		"Ser (2020)/Season 01/Ser S01E02.mkv",
		"Ser (2020)/Season 1 Bonus/Ser S01E03.mkv",
		"Ser (2020)/S26E01/inner.mkv",
		"Ser (2020)/Season 01/Ser E5E6.mkv", "Ser (2020)/Season 01/Ser E5-E6.mkv",
	)
	items, _, st := scanAll(t, root, storage.CollectionTypeTVShows)
	ep := items["Ser S01E01.mkv"]
	vs := extrasOf(t, st, ep.ID, storage.ExtraTypeVersion)
	if len(vs) != 2 {
		t.Errorf("versions of E01 = %v", itemNames(vs))
	}
	if _, ok := items["Ser S01E01 1080p.mkv"]; ok {
		t.Error("a version is listed as an episode")
	}
	series := items["Ser (2020)"]
	merged := extrasOf(t, st, series.ID, storage.ExtraTypeMergedSeason)
	if len(merged) != 1 {
		t.Errorf("merged season folders = %v, want 1", itemNames(merged))
	}
	folder, ok := items["S26E01"]
	if !ok || folder.Type != storage.ItemTypeFolder {
		t.Errorf("S26E01 = %+v, want a Folder item", folder)
	}
	inner := items["inner.mkv"]
	if inner.ParentIndexNumber == nil || *inner.ParentIndexNumber != 26 || inner.IndexNumber == nil || *inner.IndexNumber != 1 {
		t.Errorf("inner.mkv numbers = %v/%v, want 26/1", inner.ParentIndexNumber, inner.IndexNumber)
	}
	for _, n := range []string{"Ser E5E6.mkv", "Ser E5-E6.mkv"} {
		it := items[n]
		if it.IndexNumber == nil || *it.IndexNumber != 6 {
			t.Errorf("%s IndexNumber = %v, want 6 (both stay separate episodes)", n, it.IndexNumber)
		}
	}
}

func TestRescanForgetsRemovedExtrasAndPictures(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "Gone (2001)/Gone (2001).mkv", "Gone (2001)/Gone (2001)-trailer.mkv", "Gone (2001)/fanart.jpg", "Gone (2001)/fanart-1.jpg")
	st := newTestStore(t)
	s := New(st, metadata.NoopProvider{}, Config{})
	ctx := context.Background()
	lib := storage.Library{ID: "lib-1", Name: "Lib", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	page, _ := st.QueryItems(ctx, storage.ItemFilter{LibraryID: &lib.ID, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 10})
	if len(page.Items) != 1 {
		t.Fatalf("movies = %d", len(page.Items))
	}
	id := page.Items[0].ID
	if n := len(extrasOf(t, st, id)); n != 1 {
		t.Fatalf("extras = %d, want 1", n)
	}
	if got := imagePaths(t, st, id, "Backdrop"); len(got) != 2 {
		t.Fatalf("backdrops = %v", got)
	}
	if err := os.Remove(filepath.Join(root, "Gone (2001)", "Gone (2001)-trailer.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "Gone (2001)", "fanart-1.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if n := len(extrasOf(t, st, id)); n != 0 {
		t.Errorf("extras after removal = %d, want 0", n)
	}
	if got := imagePaths(t, st, id, "Backdrop"); len(got) != 1 {
		t.Errorf("backdrops after removal = %v, want 1", got)
	}
}

func TestEpisodeNumberingForms(t *testing.T) {
	cases := []struct {
		name string
		want int // 0: none
	}{
		{"Show E5E6", 6}, {"Show E05E06E07", 7}, {"Show E5-E6", 6}, {"Show E5_E6", 6}, {"Show E5.E6", 5},
		{"Show Title3", 3}, {"Show-Title3", 3}, {"Title12", 12}, {"Show 2020 Title3", 3},
		{"Show3", 0}, {"Show Part3", 0}, {"Show Title7 Extra5", 0}, {"Title3 Show", 0}, {"Show x264", 0},
		{"Show Title3 720p", 720}, {"Show 05", 5},
	}
	for _, c := range cases {
		ef := ParseEpisodeFile(c.name + ".mkv")
		got := 0
		if ef.Episode != nil {
			got = *ef.Episode
		}
		if got != c.want {
			t.Errorf("%q: episode %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExtraNaming(t *testing.T) {
	cases := map[string]string{
		"Folder Trailer": "Folder", "Making Of": "Making Of", "Foo-trailer": "Foo", "Foo.trailer": "Foo",
		"trailer": "trailer", "Trailer 2": "Trailer 2", "Foo - Sample": "Foo -", "Foo Featurette": "Foo Featurette",
		"Foo-featurette": "Foo", "N1 (2016) Trailer": "N1", "Foo trailer 2": "Foo trailer 2",
	}
	for in, want := range cases {
		if got := extraTitle(in); got != want {
			t.Errorf("extraTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
