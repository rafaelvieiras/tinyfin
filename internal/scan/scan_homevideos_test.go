package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A home-videos library catalogs every folder as a Folder and every video
// file as a Video named after its file, whatever the nesting and naming.
func TestScanHomeVideos(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"TV/Channel One/Bumper/Channel One - Bumper - 2002 - Game.mp4",
		"TV/Channel Two/Nick Clip.mkv",
		"TV/Channel Two/Sub/Deep/Deep Clip.avi",
		"TV/Channel Two/Upper.MP4",
		"TV/Channel Two/Nfo Video.mp4",
		"TV/Channel Two/Movie Nfo Video.mp4",
		"Root Video.mp4",
		"Dotted.Name.2002.1080p.mp4",
		"Empty Folder/.keep",
		"Mixed Case/The Article.mp4",
		"Mixed Case/10 Ten.mp4",
		// left out
		"TV/Channel Two/notes.txt",
		"TV/Channel Two/song.mp3",
		".hidden/Secret.mp4",
		"@eaDir/Thumb.mp4",
		"#recycle/Gone.mp4",
		"metadata/Meta.mp4",
		".dotfile.mp4",
		"Movie-trailer.mp4",
		"Movie.sample.mkv",
		"sample.mp4",
		"extras/In Extras.mp4",
		"sample/In Sample.mp4",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "x")
	}
	writeFile(t, filepath.Join(root, "TV", "Channel Two", "Nfo Video.nfo"),
		"\xef\xbb\xbf<movie><title> Nfo Video Title </title><plot>Nfo plot</plot><year>1999</year><premiered>1999-02-03</premiered></movie>")
	writeFile(t, filepath.Join(root, "TV", "Channel Two", "movie.nfo"), "<movie><title>Folder Wide</title></movie>")

	items, _, st := scanAll(t, root, storage.CollectionTypeHomeVideos)
	type want struct {
		typ      storage.ItemType
		name     string
		sortName string
		parent   string // key of the parent folder, "" for the first level
	}
	cases := map[string]want{
		"TV":                                     {storage.ItemTypeFolder, "TV", "tv", ""},
		"Channel One":                            {storage.ItemTypeFolder, "Channel One", "channel one", "TV"},
		"Bumper":                                 {storage.ItemTypeFolder, "Bumper", "bumper", "Channel One"},
		"Channel One - Bumper - 2002 - Game.mp4": {storage.ItemTypeVideo, "Channel One - Bumper - 2002 - Game", "channel one  bumper  0000002002  game", "Bumper"},
		"Channel Two":                            {storage.ItemTypeFolder, "Channel Two", "channel two", "TV"},
		"Nick Clip.mkv":                          {storage.ItemTypeVideo, "Nick Clip", "nick clip", "Channel Two"},
		"Deep Clip.avi":                          {storage.ItemTypeVideo, "Deep Clip", "deep clip", "Deep"},
		"Upper.MP4":                              {storage.ItemTypeVideo, "Upper", "upper", "Channel Two"},
		"Nfo Video.mp4":                          {storage.ItemTypeVideo, "Nfo Video Title", "nfo video title", "Channel Two"},
		"Movie Nfo Video.mp4":                    {storage.ItemTypeVideo, "Movie Nfo Video", "movie nfo video", "Channel Two"},
		"Root Video.mp4":                         {storage.ItemTypeVideo, "Root Video", "root video", ""},
		"Dotted.Name.2002.1080p.mp4":             {storage.ItemTypeVideo, "Dotted.Name.2002.1080p", "dotted name 0000002002 0000001080p", ""},
		"Empty Folder":                           {storage.ItemTypeFolder, "Empty Folder", "empty folder", ""},
		"The Article.mp4":                        {storage.ItemTypeVideo, "The Article", "article", "Mixed Case"},
		"10 Ten.mp4":                             {storage.ItemTypeVideo, "10 Ten", "0000000010 ten", "Mixed Case"},
		"extras":                                 {storage.ItemTypeFolder, "extras", "extras", ""},
		"sample":                                 {storage.ItemTypeFolder, "sample", "sample", ""},
	}
	for key, w := range cases {
		it, ok := items[key]
		if !ok {
			t.Errorf("%s: not cataloged", key)
			continue
		}
		if it.Type != w.typ || it.Name != w.name || it.SortName != w.sortName {
			t.Errorf("%s: got %s %q sort %q, want %s %q sort %q", key, it.Type, it.Name, it.SortName, w.typ, w.name, w.sortName)
		}
		switch parent := w.parent; {
		case parent == "":
			if it.ParentID != nil {
				t.Errorf("%s: ParentID = %v, want none", key, *it.ParentID)
			}
		case it.ParentID == nil || *it.ParentID != items[parent].ID:
			t.Errorf("%s: not under %q", key, parent)
		}
		if w.typ == storage.ItemTypeVideo && it.Path == "" {
			t.Errorf("%s: no Path", key)
		}
	}
	nfo := items["Nfo Video.mp4"]
	if nfo.Overview != "Nfo plot" || nfo.ProductionYear == nil || *nfo.ProductionYear != 1999 || nfo.PremiereDate == nil {
		t.Errorf("nfo video: overview %q year %v premiere %v", nfo.Overview, nfo.ProductionYear, nfo.PremiereDate)
	}

	for _, key := range []string{"notes.txt", "song.mp3", ".hidden", "@eaDir", "#recycle", "metadata", ".dotfile.mp4",
		"Movie-trailer.mp4", "Movie.sample.mkv", "sample.mp4", "In Extras.mp4", "In Sample.mp4", "Secret.mp4"} {
		if _, ok := items[key]; ok {
			t.Errorf("%s: cataloged, want it left out", key)
		}
	}

	// A folder's descendants hang under it through the ancestor index: a
	// recursive listing of TV holds everything below it.
	tv := items["TV"]
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{ParentID: &tv.ID, Recursive: true, Limit: 100})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if page.TotalCount != 11 {
		t.Errorf("descendants of TV = %d, want 11", page.TotalCount)
	}
}
