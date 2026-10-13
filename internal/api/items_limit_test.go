package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// items.list default page size: a listing without Limit returns every
// match (the reference applies no default cap), and so does Resume.
func TestItemsListHasNoDefaultCap(t *testing.T) {
	f := newItemsFixture(t)
	ctx := context.Background()
	const extra = 150
	ticks := int64(50_000_000)
	for i := 0; i < extra; i++ {
		name := fmt.Sprintf("Bulk %03d", i)
		it := storage.Item{ID: gid("bulk-" + name), Type: storage.ItemTypeMovie, LibraryID: f.movies, Name: name, SortName: name,
			Path: fmt.Sprintf("/media/Movies/%s.mp4", name), RunTimeTicks: &ticks}
		if err := f.st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
		if err := f.st.SetPlaystate(ctx, f.alice.ID, it.ID, storage.Playstate{PlaybackPositionTicks: 10_000_000}); err != nil {
			t.Fatal(err)
		}
	}
	r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Movie")
	if len(r.Items) != extra+3 || r.Total != extra+3 {
		t.Errorf("items = %d (total %d), want %d", len(r.Items), r.Total, extra+3)
	}
	if r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Movie&Limit=10"); len(r.Items) != 10 || r.Total != extra+3 {
		t.Errorf("explicit limit: %d (total %d)", len(r.Items), r.Total)
	}
	if r := f.get(t, f.aliceTok, "/UserItems/Resume"); len(r.Items) != extra {
		t.Errorf("resume = %d, want %d", len(r.Items), extra)
	}
}
