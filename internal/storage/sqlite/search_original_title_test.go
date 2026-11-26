package sqlite

import (
	"context"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// An item's original title is part of what a name search looks at
// (search.hints.R3). Measured on the reference 10.11.11 with a movie whose
// <originaltitle> differs from both its folder and its <title>: the term
// "Titulo Original" answers that movie, and so does a word from the end of it.
func TestQueryItemsNameContainsMatchesTheOriginalTitle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "lib", Name: "Movies", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	for _, it := range []storage.Item{
		{ID: "m1", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Mega Maquinas", SortName: "mega maquinas", OriginalTitle: "Ultimate Factories", Path: "/m/mega.mp4"},
		{ID: "m2", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Other Film", SortName: "other film", Path: "/m/other.mp4"},
	} {
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatalf("UpsertItem %q: %v", it.Name, err)
		}
	}
	for _, tc := range []struct {
		term string
		want []string
	}{
		{"Ultimate", []string{"m1"}},
		{"ultimate factories", []string{"m1"}},
		{"Factory", nil},
		{"Mega", []string{"m1"}},
		{"Other", []string{"m2"}},
	} {
		res, err := st.QueryItems(ctx, storage.ItemFilter{NameContains: tc.term, Limit: 10})
		if err != nil {
			t.Fatalf("QueryItems(%q): %v", tc.term, err)
		}
		var got []string
		for _, it := range res.Items {
			got = append(got, string(it.ID))
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q = %v, want %v", tc.term, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q = %v, want %v", tc.term, got, tc.want)
				break
			}
		}
	}
}

// The original title is matched with the name's rules, so accents fold on both
// sides. The reference folds only the term there and compares it against the
// raw title, which makes an accented original title unfindable by its own
// accented text (measured: "Café Über Straße" as the original title, the term
// "Café" answering 0 there). Divergence DIV-42, deliberately lenient.
func TestQueryItemsNameContainsFoldsAccentsInTheOriginalTitle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "lib", Name: "Movies", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	if err := st.UpsertItem(ctx, storage.Item{
		ID: "m1", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Sem Nome", SortName: "sem nome",
		OriginalTitle: "Café Über Straße", Path: "/m/x.mp4",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	for _, term := range []string{"Café Über", "cafe uber", "strasse"} {
		res, err := st.QueryItems(ctx, storage.ItemFilter{NameContains: term, Limit: 10})
		if err != nil {
			t.Fatalf("QueryItems(%q): %v", term, err)
		}
		if res.TotalCount != 1 {
			t.Errorf("%q = %d results, want 1", term, res.TotalCount)
		}
	}
}
