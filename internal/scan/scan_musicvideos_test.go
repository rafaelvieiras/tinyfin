package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A music-videos library catalogs every video file as a MusicVideo named after
// its file; a folder with a single clip and no sub folder is that clip, other
// folders are Folder items, -partN files are one clip, and a <musicvideo> nfo
// gives artists, album, year and genres.
func TestScanMusicVideos(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"Loose (2020).mp4",
		"Artist/One.mp4",
		"Artist/Two.mkv",
		"Single/Album/Solo.mp4",
		"Mixed/A.mp4",
		"Mixed/Sub/B.mp4",
		"Parts/Song - part1.mp4",
		"Parts/Song - part2.mp4",
		"Versions/Song.mp4",
		"Versions/Song - 4K.mp4",
		".hidden/Secret.mp4",
		"Artist/notes.txt",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	writeFile(t, filepath.Join(root, "Artist", "Two.nfo"),
		"<musicvideo><title>Second</title><artist>A One</artist><artist>A Two</artist><album>The Album</album><year>1999</year><genre>Pop</genre><plot>Plot</plot></musicvideo>")

	items, _, st := scanAll(t, root, storage.CollectionTypeMusicVideos)
	want := map[string]struct {
		typ    storage.ItemType
		name   string
		parent string
	}{
		"Loose (2020).mp4": {storage.ItemTypeMusicVideo, "Loose (2020)", ""},
		"Artist":           {storage.ItemTypeFolder, "Artist", ""},
		"One.mp4":          {storage.ItemTypeMusicVideo, "One", "Artist"},
		"Two.mkv":          {storage.ItemTypeMusicVideo, "Second", "Artist"},
		"Solo.mp4":         {storage.ItemTypeMusicVideo, "Solo", ""},
		"Mixed":            {storage.ItemTypeFolder, "Mixed", ""},
		"A.mp4":            {storage.ItemTypeMusicVideo, "A", "Mixed"},
		"B.mp4":            {storage.ItemTypeMusicVideo, "B", "Mixed"},
		"Song - part1.mp4": {storage.ItemTypeMusicVideo, "Song - part1", "Parts"},
		"Versions":         {storage.ItemTypeFolder, "Versions", ""},
		"Song.mp4":         {storage.ItemTypeMusicVideo, "Song", "Versions"},
		"Song - 4K.mp4":    {storage.ItemTypeMusicVideo, "Song - 4K", "Versions"},
	}
	// "Parts" holds one stacked clip and "Single/Album" one clip: neither is an item.
	want["Song - part1.mp4"] = struct {
		typ    storage.ItemType
		name   string
		parent string
	}{storage.ItemTypeMusicVideo, "Song - part1", ""}
	for _, folder := range []string{"Parts", "Album"} {
		if _, ok := items[folder]; ok {
			t.Errorf("%s: cataloged, want it collapsed into its clip", folder)
		}
	}
	// "Single" holds only a folder: it stays a Folder item, and Solo is its child.
	want["Single"] = struct {
		typ    storage.ItemType
		name   string
		parent string
	}{storage.ItemTypeFolder, "Single", ""}
	want["Solo.mp4"] = struct {
		typ    storage.ItemType
		name   string
		parent string
	}{storage.ItemTypeMusicVideo, "Solo", "Single"}
	for key, w := range want {
		it, ok := items[key]
		if !ok {
			t.Errorf("%s: not cataloged", key)
			continue
		}
		if it.Type != w.typ || it.Name != w.name {
			t.Errorf("%s: got %s %q, want %s %q", key, it.Type, it.Name, w.typ, w.name)
		}
		switch {
		case w.parent == "":
			if it.ParentID != nil {
				t.Errorf("%s: ParentID = %v, want none", key, *it.ParentID)
			}
		case it.ParentID == nil || *it.ParentID != items[w.parent].ID:
			t.Errorf("%s: not under %q", key, w.parent)
		}
	}
	for _, key := range []string{"Song - part2.mp4", "Secret.mp4", "notes.txt", ".hidden"} {
		if _, ok := items[key]; ok {
			t.Errorf("%s: cataloged, want it left out", key)
		}
	}
	if y := items["Loose (2020).mp4"].ProductionYear; y == nil || *y != 2020 {
		t.Errorf("year of the loose clip = %v, want 2020", y)
	}
	two := items["Two.mkv"]
	if len(two.Artists) != 2 || two.Artists[0] != "A One" || two.Album != "The Album" || two.Overview != "Plot" ||
		two.ProductionYear == nil || *two.ProductionYear != 1999 || len(two.Genres) != 1 {
		t.Errorf("nfo clip: artists %v album %q overview %q year %v genres %v", two.Artists, two.Album, two.Overview, two.ProductionYear, two.Genres)
	}

	// The part is an owned item of the first part.
	part := items["Song - part1.mp4"]
	extras, err := st.QueryItems(context.Background(), storage.ItemFilter{OwnerID: &part.ID, IncludeExtras: true, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if extras.TotalCount != 1 {
		t.Errorf("parts owned by the stacked clip = %d, want 1", extras.TotalCount)
	}

	// The artists of the clips are artist items of the library.
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{
		ItemTypes: []storage.ItemType{storage.ItemTypeMusicArtist}, IncludeExtras: true, Recursive: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if page.TotalCount != 2 {
		t.Errorf("artist items = %d, want 2", page.TotalCount)
	}
	for _, a := range page.Items {
		if len(a.Genres) != 1 || a.Genres[0] != "Pop" {
			t.Errorf("artist %q genres %v, want [Pop]", a.Name, a.Genres)
		}
	}
}
