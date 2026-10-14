package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func collFixture(t *testing.T) (*Store, storage.LibraryID) {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	lib := storage.Library{ID: "lib1", Name: "Movies", CollectionType: storage.CollectionTypeMovies}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLibrary(ctx, storage.Library{ID: "boxes", Name: "Collections", CollectionType: storage.CollectionTypeBoxSets}); err != nil {
		t.Fatal(err)
	}
	return st, lib.ID
}

func putItem(t *testing.T, st *Store, id string, typ storage.ItemType, lib storage.LibraryID, parent *storage.ItemID, name string) {
	t.Helper()
	if err := st.UpsertItem(context.Background(), storage.Item{ID: storage.ItemID(id), Type: typ, LibraryID: lib, ParentID: parent, Name: name}); err != nil {
		t.Fatal(err)
	}
}

func ancestorsOf(t *testing.T, st *Store, id string) []string {
	t.Helper()
	rows, err := st.db.Query(`SELECT ancestor_id FROM item_ancestors WHERE item_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func TestCollectionMembershipAndAncestors(t *testing.T) {
	st, lib := collFixture(t)
	ctx := context.Background()
	show := storage.ItemID("show")
	season := storage.ItemID("season")
	putItem(t, st, "m1", storage.ItemTypeMovie, lib, nil, "One")
	putItem(t, st, "m2", storage.ItemTypeMovie, lib, nil, "Two")
	putItem(t, st, "show", storage.ItemTypeSeries, lib, nil, "Show")
	putItem(t, st, "season", storage.ItemTypeSeason, lib, &show, "S1")
	putItem(t, st, "ep", storage.ItemTypeEpisode, lib, &season, "E1")
	putItem(t, st, "box", storage.ItemTypeBoxSet, "boxes", nil, "Box")
	putItem(t, st, "outer", storage.ItemTypeBoxSet, "boxes", nil, "Outer")

	added, err := st.AddToCollection(ctx, "box", []storage.ItemID{"m2", "show", "m2", "box"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(added, []storage.ItemID{"m2", "show"}) {
		t.Fatalf("added = %v", added)
	}
	if again, _ := st.AddToCollection(ctx, "box", []storage.ItemID{"m2"}); len(again) != 0 {
		t.Fatalf("second add reported %v", again)
	}
	members, _ := st.CollectionMembers(ctx, "box")
	if !reflect.DeepEqual(members, []storage.ItemID{"m2", "show"}) {
		t.Fatalf("members = %v", members)
	}
	// Everything below a member has the box as an ancestor.
	for _, id := range []string{"m2", "show", "season", "ep"} {
		if got := ancestorsOf(t, st, id); !contains(got, "box") {
			t.Errorf("%s ancestors = %v, want box among them", id, got)
		}
	}
	if got := ancestorsOf(t, st, "m1"); contains(got, "box") {
		t.Errorf("m1 ancestors = %v", got)
	}
	// A box inside a box passes its members up.
	if _, err := st.AddToCollection(ctx, "outer", []storage.ItemID{"box"}); err != nil {
		t.Fatal(err)
	}
	if got := ancestorsOf(t, st, "ep"); !contains(got, "outer") || !contains(got, "box") {
		t.Errorf("ep ancestors = %v, want outer and box", got)
	}
	// Rewriting an item row keeps its collection ancestors.
	putItem(t, st, "season", storage.ItemTypeSeason, lib, &show, "S1 renamed")
	putItem(t, st, "ep", storage.ItemTypeEpisode, lib, &season, "E1 renamed")
	if got := ancestorsOf(t, st, "ep"); !contains(got, "outer") || !contains(got, "box") || !contains(got, "season") || !contains(got, "show") {
		t.Errorf("ep ancestors after upsert = %v", got)
	}
	// A member added to the inner box shows up in the outer one.
	if _, err := st.AddToCollection(ctx, "box", []storage.ItemID{"m1"}); err != nil {
		t.Fatal(err)
	}
	if got := ancestorsOf(t, st, "m1"); !contains(got, "outer") {
		t.Errorf("m1 ancestors = %v, want outer", got)
	}
	// Removing a member takes its rows away.
	if err := st.RemoveFromCollection(ctx, "box", []storage.ItemID{"show", "nobody"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"show", "season", "ep"} {
		got := ancestorsOf(t, st, id)
		if contains(got, "box") || contains(got, "outer") {
			t.Errorf("%s ancestors after removal = %v", id, got)
		}
	}
	// SetCollectionMembers replaces the membership.
	if err := st.SetCollectionMembers(ctx, "box", []storage.ItemID{"show"}); err != nil {
		t.Fatal(err)
	}
	if members, _ := st.CollectionMembers(ctx, "box"); !reflect.DeepEqual(members, []storage.ItemID{"show"}) {
		t.Fatalf("members = %v", members)
	}
	if got := ancestorsOf(t, st, "m2"); contains(got, "box") {
		t.Errorf("m2 still under box: %v", got)
	}
	// Deleting the box clears links and rows; members stay.
	if err := st.DeleteItem(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	if got := ancestorsOf(t, st, "ep"); contains(got, "box") {
		t.Errorf("ep ancestors = %v", got)
	}
	if _, err := st.GetItem(ctx, "show"); err != nil {
		t.Errorf("member deleted with the box: %v", err)
	}
	// Deleting a member drops its link.
	if _, err := st.AddToCollection(ctx, "outer", []storage.ItemID{"m1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteItem(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if members, _ := st.CollectionMembers(ctx, "outer"); len(members) != 0 {
		t.Errorf("members after deleting m1 = %v", members)
	}
}

func TestCollectionQueryFilters(t *testing.T) {
	st, lib := collFixture(t)
	ctx := context.Background()
	putItem(t, st, "m1", storage.ItemTypeMovie, lib, nil, "One")
	putItem(t, st, "m2", storage.ItemTypeMovie, lib, nil, "Two")
	putItem(t, st, "m3", storage.ItemTypeMovie, lib, nil, "Three")
	putItem(t, st, "box", storage.ItemTypeBoxSet, "boxes", nil, "Box")
	putItem(t, st, "gone", storage.ItemTypeBoxSet, "boxes", nil, "Gone")
	if _, err := st.AddToCollection(ctx, "box", []storage.ItemID{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddToCollection(ctx, "gone", []storage.ItemID{"m3"}); err != nil {
		t.Fatal(err)
	}
	names := func(f storage.ItemFilter) string {
		t.Helper()
		f.Limit = 100
		f.SortBy = []storage.SortField{{By: storage.SortByName}}
		page, err := st.QueryItems(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range page.Items {
			out = append(out, it.Name)
		}
		return join(out)
	}
	box := storage.ItemID("box")
	if got := names(storage.ItemFilter{ParentID: &box}); got != "One,Two" {
		t.Errorf("children = %s", got)
	}
	if got := names(storage.ItemFilter{ParentID: &box, Recursive: true}); got != "One,Two" {
		t.Errorf("recursive = %s", got)
	}
	libID := lib
	if got := names(storage.ItemFilter{LibraryID: &libID, Recursive: true, LibraryBoxSets: true,
		ItemTypes: []storage.ItemType{storage.ItemTypeBoxSet}}); got != "Box,Gone" {
		t.Errorf("boxes of the library = %s", got)
	}
	// Collapse: members give way to the boxes that hold them.
	if got := names(storage.ItemFilter{CollapseBoxSets: true, ItemTypes: []storage.ItemType{storage.ItemTypeMovie}}); got != "Box,Gone" {
		t.Errorf("collapsed = %s", got)
	}
	// With access to the movies library only, the boxes are not visible and
	// nothing collapses.
	if got := names(storage.ItemFilter{CollapseBoxSets: true, ItemTypes: []storage.ItemType{storage.ItemTypeMovie},
		RestrictToLibraries: true, LibraryIDs: []storage.LibraryID{lib}}); got != "One,Three,Two" {
		t.Errorf("collapsed without the boxes library = %s", got)
	}
	// With both libraries, a box none of whose members is visible is hidden.
	if got := names(storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeBoxSet},
		RestrictToLibraries: true, LibraryIDs: []storage.LibraryID{"boxes"}}); got != "" {
		t.Errorf("boxes with no visible member = %q", got)
	}
}

func TestMoviesWithSetAndMigration(t *testing.T) {
	st, lib := collFixture(t)
	ctx := context.Background()
	for _, it := range []storage.Item{
		{ID: "a", Type: storage.ItemTypeMovie, LibraryID: lib, Name: "A", SetName: "S", SortName: "a"},
		{ID: "b", Type: storage.ItemTypeMovie, LibraryID: lib, Name: "B", SetName: "S"},
		{ID: "c", Type: storage.ItemTypeMovie, LibraryID: lib, Name: "C", SetName: "T"},
		{ID: "d", Type: storage.ItemTypeMovie, LibraryID: lib, Name: "D"},
	} {
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := st.MoviesWithSet(ctx, []storage.LibraryID{lib})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "S" || len(groups[0].Members) != 2 || groups[1].Name != "T" {
		t.Fatalf("groups = %+v", groups)
	}
	got, _ := st.GetItem(ctx, "a")
	if got.SetName != "S" {
		t.Errorf("SetName = %q", got.SetName)
	}
	// Reopening the file keeps the column (the migration is not re-applied).
	path := filepath.Join(t.TempDir(), "x.db")
	if s2, err := New(path); err != nil {
		t.Fatal(err)
	} else {
		s2.Close()
	}
	if s3, err := New(path); err != nil {
		t.Fatal(err)
	} else {
		s3.Close()
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
