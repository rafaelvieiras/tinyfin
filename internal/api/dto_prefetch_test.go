package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestItemsListingCostDoesNotGrowWithThePage pins the property the listing
// route broke: GET /Items answered one page with a fixed number of store reads,
// whatever the page holds.
//
// Measured on a copy of the production database (v0.3.5, before this test): a
// 100-episode page issued 4,900 picture lookups, 200 ancestor lookups and a
// playstate query per card — about 5,200 statements — because every card asked
// for its own pictures, then asked for each ancestor's pictures three times
// over to fill in the inherited-logo/thumb/backdrop fields. The same page now
// costs four statements, and 600 episodes cost the same four.
//
// The assertions are structural, not timed: a wall clock cannot say which of
// those 5,200 statements came back.
func TestItemsListingCostDoesNotGrowWithThePage(t *testing.T) {
	limits := []int{20, 100}
	costs := make([]int, len(limits))
	tallies := make([]map[string]int, len(limits))

	for i, limit := range limits {
		st := openTestStore(t)
		cs := &countingStore{Store: st}
		ts := startTestServerWithStore(t, cs)

		user := createTestUser(t, st, "frank", "frankpass")
		token := loginToken(t, ts.URL, user.Name, "frankpass")
		get := authedClient(t, ts.URL, token)

		libID := createTestLibrary(t, st, fmt.Sprintf("Listing %03d", limit), storage.CollectionTypeTVShows)
		now := time.Now().UTC()
		seriesID := upsertSeries(t, st, libID, "Listing Show", now)
		seasonID := upsertSeason(t, st, libID, seriesID, "Listing Show Season 1", 1, now)
		for n := 1; n <= 120; n++ {
			idx, name := n, fmt.Sprintf("LS01E%03d", n)
			if err := st.UpsertItem(context.Background(), storage.Item{
				Type: storage.ItemTypeEpisode, LibraryID: libID, ParentID: &seasonID,
				SeriesID: &seriesID, SeasonID: &seasonID, Name: name, IndexNumber: &idx,
				Path: "/media/series/" + name + ".mkv", DateCreated: now,
			}); err != nil {
				t.Fatalf("UpsertItem episode %q: %v", name, err)
			}
		}
		path := fmt.Sprintf("/Items?parentId=%s&includeItemTypes=Episode&recursive=true&limit=%d&userId=%s&fields=ChildCount,RecursiveItemCount,UnplayedItemCount,DateCreated",
			libID, limit, user.ID)

		// One warm-up request keeps statement preparation out of the tally.
		if resp := get(http.MethodGet, path); resp.StatusCode != http.StatusOK {
			t.Fatalf("warm-up GET %s status = %d", path, resp.StatusCode)
		} else {
			resp.Body.Close()
		}
		cs.reset()
		resp := get(http.MethodGet, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}
		resp.Body.Close()
		costs[i] = cs.total()
		tallies[i] = cs.tally()
	}

	for i, limit := range limits {
		for _, name := range dtoPerItemCalls {
			if tallies[i][name] != 0 {
				t.Errorf("limit=%d: %s called %d times, want 0 — the page's cards share their answers (%v)",
					limit, name, tallies[i][name], tallies[i])
			}
		}
		if costs[i] > maxGroupedRowCalls {
			t.Errorf("limit=%d: the page cost %d store reads (%v), want at most %d",
				limit, costs[i], tallies[i], maxGroupedRowCalls)
		}
	}
	if costs[0] != costs[1] {
		t.Errorf("cost depends on the page size: %d cards cost %d store reads (%v), %d cards cost %d (%v)",
			limits[0], costs[0], tallies[0], limits[1], costs[1], tallies[1])
	}
}
