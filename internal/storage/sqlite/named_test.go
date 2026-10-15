package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func namedFixture(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "lib", Name: "Movies", CollectionType: storage.CollectionTypeMovies}); err != nil {
		t.Fatal(err)
	}
	y := func(n int) *int { return &n }
	series := storage.ItemID("series")
	for _, it := range []storage.Item{
		{ID: "m1", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Zeta Quest", SortName: "zeta quest", ProductionYear: y(2011),
			Genres: []string{"Action", "Comedy"}, Studios: []string{"Acme"}, People: []storage.Person{{Name: "Ann Actor", Type: "Actor"}, {Name: "Dan Director", Type: "Director"}}},
		{ID: "m2", Type: storage.ItemTypeMovie, LibraryID: "lib", Name: "Yankee Dawn", SortName: "yankee dawn", ProductionYear: y(2012),
			Genres: []string{"action"}, People: []storage.Person{{Name: "Ann Actor", Type: "Actor"}}},
		{ID: "series", Type: storage.ItemTypeSeries, LibraryID: "lib", Name: "A Show", SortName: "show", ProductionYear: y(2012),
			Genres: []string{"Drama"}, Studios: []string{"Acme"}},
		{ID: "ep", Type: storage.ItemTypeEpisode, LibraryID: "lib", Name: "Pilot", SortName: "001 - 0001 - pilot", ParentID: &series, SeriesID: &series},
	} {
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestQueryNamedCountsAndOrder(t *testing.T) {
	st := namedFixture(t)
	ctx := context.Background()
	scope := storage.ItemFilter{}
	page, err := st.QueryNamed(ctx, storage.NamedQuery{Kind: storage.NamedGenre, Scope: scope, ByName: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Entries {
		got = append(got, e.Name)
	}
	// "Action" and "action" are one genre; the episode of a series is not a
	// reason for the name to exist.
	if want := []string{"Action", "Comedy", "Drama"}; !reflect.DeepEqual(got, want) || page.Total != 3 {
		t.Fatalf("genres = %v (%d)", got, page.Total)
	}
	var drama storage.NamedEntry
	for _, e := range page.Entries {
		if e.Name == "Drama" {
			drama = e
		}
	}
	// The series carries Drama; its episode is counted under it.
	if drama.Series != 1 || drama.Episodes != 1 || drama.Total != 2 {
		t.Fatalf("Drama counts = %+v", drama)
	}
	// A type filter narrows the listed names, not what an entry counts.
	page, err = st.QueryNamed(ctx, storage.NamedQuery{Kind: storage.NamedGenre, Scope: storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeEpisode}}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("genres of episodes = %d, want 0", page.Total)
	}
	// People by credit type and the years in order of first appearance.
	page, err = st.QueryNamed(ctx, storage.NamedQuery{Kind: storage.NamedPerson, Scope: scope, ByName: true, PersonTypes: []string{"Director"}})
	if err != nil || page.Total != 1 || page.Entries[0].Name != "Dan Director" {
		t.Fatalf("directors = %+v (%v)", page, err)
	}
	page, err = st.QueryNamed(ctx, storage.NamedQuery{Kind: storage.NamedYear, Scope: scope, ByName: true, Descending: true})
	if err != nil || page.Total != 2 || page.Entries[0].Name != "2012" {
		t.Fatalf("years = %+v (%v)", page, err)
	}
}

func TestFindNamedBackfillsAnOldDatabase(t *testing.T) {
	st := namedFixture(t)
	ctx := context.Background()
	if _, err := st.db.Exec(`DELETE FROM named_items`); err != nil {
		t.Fatal(err)
	}
	ref, ok, err := st.FindNamed(ctx, storage.NamedID(storage.NamedStudio, "ACME"))
	if err != nil || !ok || ref.Kind != storage.NamedStudio || ref.Name != "Acme" {
		t.Fatalf("FindNamed = %+v %v %v", ref, ok, err)
	}
	if _, ok, _ := st.FindNamed(ctx, storage.NamedID(storage.NamedGenre, "Nothing")); ok {
		t.Fatal("found a name nothing carries")
	}
}

func TestSearchHintsRanking(t *testing.T) {
	st := namedFixture(t)
	ctx := context.Background()
	page, err := st.SearchHints(ctx, storage.SearchQuery{
		Items: storage.ItemFilter{}, Term: "a",
		Kinds: []storage.NamedKind{storage.NamedGenre, storage.NamedStudio, storage.NamedPerson}, KeepPersonCase: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range page.Hits {
		switch {
		case h.Item != nil:
			got = append(got, h.Item.Name)
		case h.Named != nil:
			got = append(got, h.Named.Name)
		}
	}
	// Names starting with "a" first (the person, written with a capital,
	// sorts ahead of the lower-case sort names), then those with "a" ending a
	// word, then the rest (the person again first).
	want := []string{"Ann Actor", "Acme", "Action", "A Show", "Zeta Quest", "Dan Director", "Drama", "Yankee Dawn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hits = %v, want %v", got, want)
	}
	// A term with no letter or digit finds nothing.
	page, err = st.SearchHints(ctx, storage.SearchQuery{Items: storage.ItemFilter{}, Term: "..."})
	if err != nil || page.Total != 0 {
		t.Fatalf("punctuation = %+v %v", page, err)
	}
}

func TestPlaylistAccessAndEntries(t *testing.T) {
	st := namedFixture(t)
	ctx := context.Background()
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "pl", Name: "Playlists", CollectionType: storage.CollectionTypePlaylists}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertItem(ctx, storage.Item{ID: "p1", Type: storage.ItemTypePlaylist, LibraryID: "pl", Name: "Mix"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPlaylistAccess(ctx, "p1"); err == nil {
		t.Fatal("access before it was written")
	}
	acc := storage.PlaylistAccess{PlaylistID: "p1", OwnerID: "u1", OpenAccess: false, MediaType: "Video",
		Shares: []storage.PlaylistShare{{UserID: "u2", CanEdit: true}, {UserID: "u3"}}}
	if err := st.SetPlaylistAccess(ctx, acc); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPlaylistAccess(ctx, "p1")
	if err != nil || !reflect.DeepEqual(got, acc) {
		t.Fatalf("access = %+v %v", got, err)
	}
	if err := st.SetPlaylistEntries(ctx, "p1", []storage.ItemID{"m1", "m2", "m1"}); err != nil {
		t.Fatal(err)
	}
	ents, _ := st.PlaylistEntries(ctx, "p1")
	if !reflect.DeepEqual(ents, []storage.ItemID{"m1", "m2", "m1"}) {
		t.Fatalf("entries = %v", ents)
	}
	// Only the readers see the playlist in a listing.
	for _, tc := range []struct {
		viewer storage.UserID
		want   int
	}{{"u1", 1}, {"u2", 1}, {"u9", 0}} {
		v := tc.viewer
		page, err := st.QueryItems(ctx, storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypePlaylist}, PlaylistViewer: &v, Limit: 10})
		if err != nil || page.TotalCount != tc.want {
			t.Errorf("viewer %s sees %d (%v), want %d", v, page.TotalCount, err, tc.want)
		}
	}
	// Deleting the playlist item removes its access record and entries.
	if err := st.DeleteItem(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPlaylistAccess(ctx, "p1"); err == nil {
		t.Fatal("access survived the playlist")
	}
}
