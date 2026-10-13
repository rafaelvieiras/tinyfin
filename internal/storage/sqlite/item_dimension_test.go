package sqlite

import (
	"context"
	"sort"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// items.filter.R17: IsHd / Is4K select resolution buckets and combine as a
// union; Is4K=false adds nothing.
func TestQueryItemsResolutionBuckets(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, _, _, _, _ := seedTree(t, st)

	sizes := map[string][2]int{
		"sd": {320, 240}, "edge1198": {1198, 100}, "edge1200": {1200, 100}, "hd": {1280, 720},
		"edge3800": {3800, 100}, "uhd": {3840, 2160}, "tall": {100, 2100}, "mixed": {1300, 2200},
	}
	for name, wh := range sizes {
		w, h := wh[0], wh[1]
		if err := st.UpsertItem(ctx, storage.Item{
			Type: storage.ItemTypeMovie, LibraryID: libraryID, Name: "dim-" + name, Path: "/media/" + name + ".mp4",
			MediaStreams: []storage.MediaStreamRef{{Type: storage.StreamTypeVideo, Codec: "h264", Width: &w, Height: &h}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	tr, fa := true, false
	cases := []struct {
		name string
		f    storage.ItemFilter
		want []string
	}{
		{"hd", storage.ItemFilter{IsHD: &tr}, []string{"edge1200", "hd"}},
		{"sd", storage.ItemFilter{IsHD: &fa}, []string{"edge1198", "sd", "tall"}},
		{"4k", storage.ItemFilter{Is4K: &tr}, []string{"edge3800", "mixed", "tall", "uhd"}},
		{"4k false alone", storage.ItemFilter{Is4K: &fa}, nil},
		{"hd and not 4k", storage.ItemFilter{IsHD: &tr, Is4K: &fa}, []string{"edge1200", "hd"}},
		{"sd and not 4k", storage.ItemFilter{IsHD: &fa, Is4K: &fa}, []string{"edge1198", "sd", "tall"}},
		{"hd or 4k", storage.ItemFilter{IsHD: &tr, Is4K: &tr}, []string{"edge1200", "edge3800", "hd", "mixed", "tall", "uhd"}},
		{"sd or 4k", storage.ItemFilter{IsHD: &fa, Is4K: &tr}, []string{"edge1198", "edge3800", "mixed", "sd", "tall", "uhd"}},
	}
	for _, c := range cases {
		f := c.f
		f.Recursive = true
		f.ItemTypes = []storage.ItemType{storage.ItemTypeMovie}
		f.Limit = 100
		res, err := st.QueryItems(ctx, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got []string
		for _, it := range res.Items {
			if len(it.Name) > 4 && it.Name[:4] == "dim-" {
				got = append(got, it.Name[4:])
			}
		}
		sort.Strings(got)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
