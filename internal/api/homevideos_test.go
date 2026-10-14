package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A home-videos library is browsed as folders and videos: the library lists
// its first level, a folder lists its children, Video items are playable
// leaves, and the IsFolder query parameter has no effect (items.filter.R20).
func TestHomeVideosLibraryBrowsing(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "hvuser", "hvpass")
	token := loginToken(t, ts.URL, user.Name, "hvpass")
	get := authedClient(t, ts.URL, token)

	root := t.TempDir()
	for _, rel := range []string{"TV/Channel One/Bumper/Game.mp4", "TV/Nick.mp4", "Root Video.mp4"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := storage.Library{ID: "0123456789abcdef0123456789abcdef", Name: "Home", CollectionType: storage.CollectionTypeHomeVideos, Paths: []string{root}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.New(st, metadata.NoopProvider{}, scan.Config{}).ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	list := func(path string) (total int, items []baseItemDto) {
		t.Helper()
		resp := get(http.MethodGet, path)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		var out struct {
			Items            []baseItemDto
			TotalRecordCount int
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.TotalRecordCount, out.Items
	}
	names := func(items []baseItemDto) map[string]string {
		m := map[string]string{}
		for _, it := range items {
			m[it.Name] = it.Type
		}
		return m
	}

	// First level: the folder and the video, not the root folder item.
	total, first := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Fields=ChildCount")
	if got := names(first); total != 2 || got["TV"] != "Folder" || got["Root Video"] != "Video" {
		t.Fatalf("library first level = %v (total %d)", got, total)
	}
	var tvID string
	for _, it := range first {
		if it.Name == "TV" {
			tvID = it.ID
			if it.ChildCount == nil || *it.ChildCount != 2 || !it.IsFolder || it.MediaSources != nil {
				t.Errorf("TV folder: ChildCount %v IsFolder %v MediaSources %v", it.ChildCount, it.IsFolder, it.MediaSources)
			}
		}
	}
	_, children := list("/Items?ParentId=" + tvID)
	if got := names(children); len(got) != 2 || got["Channel One"] != "Folder" || got["Nick"] != "Video" {
		t.Errorf("TV children = %v", got)
	}

	// Videos and folders, recursively, with the type and media type filters.
	if total, _ := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true"); total != 7 {
		t.Errorf("recursive total = %d, want 7 (the root folder, three folders, three videos)", total)
	}
	if total, _ := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true&IncludeItemTypes=Video"); total != 3 {
		t.Errorf("IncludeItemTypes=Video total = %d, want 3", total)
	}
	if total, items := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true&MediaTypes=Video"); total != 3 || items[0].MediaType != "Video" {
		t.Errorf("MediaTypes=Video total = %d", total)
	}
	if total, _ := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true&Filters=IsNotFolder"); total != 3 {
		t.Errorf("Filters=IsNotFolder total = %d, want 3", total)
	}
	all, _ := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true")
	for _, q := range []string{"IsFolder=true", "IsFolder=false", "IsFolder=junk"} {
		if total, _ := list("/Items?ParentId=0123456789abcdef0123456789abcdef&Recursive=true&" + q); total != all {
			t.Errorf("%s changed the result: %d, want %d", q, total, all)
		}
	}

	// A folder is not playable.
	resp := get(http.MethodPost, "/Items/"+tvID+"/PlaybackInfo")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PlaybackInfo of a folder = %d, want 400", resp.StatusCode)
	}
}

// Seasons list by number with the season that has no number last; episodes
// list by season (an episode without a season number takes its season's) and
// then by sort name.
func TestShowSeasonsAndEpisodesOrderWithoutNumbers(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "ordrer", "ordpass")
	token := loginToken(t, ts.URL, user.Name, "ordpass")
	get := authedClient(t, ts.URL, token)
	libID := createTestLibrary(t, st, "OrderShows", storage.CollectionTypeTVShows)
	ctx := context.Background()

	put := func(it storage.Item) storage.ItemID {
		t.Helper()
		it.LibraryID = libID
		if it.ID == "" {
			it.ID = storage.ItemID("0123456789abcdef0123456789" + it.Name[:6])
		}
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatalf("UpsertItem %q: %v", it.Name, err)
		}
		return it.ID
	}
	num := func(n int) *int { return &n }
	series := put(storage.Item{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", Type: storage.ItemTypeSeries, Name: "Order Show"})
	unknown := put(storage.Item{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", Type: storage.ItemTypeSeason, Name: "Season Unknown", ParentID: &series, SeriesID: &series})
	two := put(storage.Item{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3", Type: storage.ItemTypeSeason, Name: "Season 2", IndexNumber: num(2), ParentID: &series, SeriesID: &series})
	one := put(storage.Item{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4", Type: storage.ItemTypeSeason, Name: "Season 1", IndexNumber: num(1), ParentID: &series, SeriesID: &series})
	ep := func(id string, name string, season storage.ItemID, parentIdx, idx *int) {
		put(storage.Item{ID: storage.ItemID(id), Type: storage.ItemTypeEpisode, Name: name, SortName: name,
			ParentID: &season, SeriesID: &series, SeasonID: &season, ParentIndexNumber: parentIdx, IndexNumber: idx})
	}
	ep("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1", "Loose Unknown", unknown, nil, nil)
	ep("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2", "Second Season", two, num(2), num(1))
	ep("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb3", "Loose In One", one, nil, nil)
	ep("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb4", "Numbered One", one, num(1), num(1))

	read := func(path string) []baseItemDto {
		t.Helper()
		resp := get(http.MethodGet, path)
		defer resp.Body.Close()
		var out struct{ Items []baseItemDto }
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Items
	}
	var got []string
	for _, it := range read("/Shows/" + string(series) + "/Seasons") {
		got = append(got, it.Name)
	}
	if want := []string{"Season 1", "Season 2", "Season Unknown"}; !equalStrings(got, want) {
		t.Errorf("seasons = %v, want %v", got, want)
	}
	got = nil
	for _, it := range read("/Shows/" + string(series) + "/Episodes") {
		got = append(got, it.Name)
	}
	// Season 1 orders by sort name: "001 - 0001 - Numbered One" before the
	// file-named "Loose In One"; the season without a number comes last.
	if want := []string{"Numbered One", "Loose In One", "Second Season", "Loose Unknown"}; !equalStrings(got, want) {
		t.Errorf("episodes = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
