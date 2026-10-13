package sqlite

import (
	"context"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// items.sort.R8: the stored sort name is the normalized key, so ordering by
// it (and the SortName field) follows the contract rules.
func TestUpsertItemStoresNormalizedSortName(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	libraryID, _, _, _, _ := seedTree(t, st)

	one := 1
	cases := []struct {
		it   storage.Item
		want string
	}{
		{storage.Item{Type: storage.ItemTypeMovie, Name: "The Zeta Sort (2012)", SortName: "The Zeta Sort (2012)"}, "zeta sort (0000002012)"},
		{storage.Item{Type: storage.ItemTypeMovie, Name: "Éclair"}, "eclair"},
		{storage.Item{Type: storage.ItemTypeEpisode, Name: "The Opening", ParentIndexNumber: &one, IndexNumber: &one}, "001 - 0001 - The Opening"},
	}
	for _, c := range cases {
		c.it.ID = storage.ItemID(newID())
		c.it.LibraryID = libraryID
		if err := st.UpsertItem(ctx, c.it); err != nil {
			t.Fatalf("UpsertItem: %v", err)
		}
		got, err := st.GetItem(ctx, c.it.ID)
		if err != nil {
			t.Fatalf("GetItem: %v", err)
		}
		if got.SortName != c.want {
			t.Errorf("%q: stored SortName = %q, want %q", c.it.Name, got.SortName, c.want)
		}
		if got.Name != c.it.Name {
			t.Errorf("Name changed: %q", got.Name)
		}
	}
}
