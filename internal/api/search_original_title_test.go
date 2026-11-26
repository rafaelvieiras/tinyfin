package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// The original title is searched like the name, through both routes the
// clients use (search.hints.R3, items.search.R1). Measured on the reference
// 10.11.11: a movie named "Nome Interno" with <originaltitle>Titulo Original
// Unico</originaltitle> answers the term "Titulo Original" on /Search/Hints
// and on /Items?SearchTerm=, and answered nothing before this test existed.
func TestSearchMatchesTheOriginalTitle(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "root", "rootpass")
	lib := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	tok := loginToken(t, ts.URL, "root", "rootpass")
	for _, it := range []struct{ name, original string }{
		{"Mega Maquinas", "Ultimate Factories"},
		{"Other Film", ""},
	} {
		if err := st.UpsertItem(context.Background(), storage.Item{
			Type: storage.ItemTypeMovie, LibraryID: lib, Name: it.name, SortName: it.name,
			OriginalTitle: it.original, Path: "/m/" + it.name + ".mp4",
		}); err != nil {
			t.Fatalf("UpsertItem %q: %v", it.name, err)
		}
	}
	e := &plEnv{t: t, base: ts.URL, st: st}
	for _, tc := range []struct {
		q    string
		want string
	}{
		{"searchTerm=Ultimate", "Movie:Mega Maquinas"},
		{"searchTerm=ultimate%20factories", "Movie:Mega Maquinas"},
		{"searchTerm=maquinas", "Movie:Mega Maquinas"},
		{"searchTerm=Other", "Movie:Other Film"},
		{"searchTerm=Factory", ""},
	} {
		names, _, code := e.hints(tok, tc.q)
		if code != 200 || strings.Join(names, ",") != tc.want {
			t.Errorf("%s: %d %q, want 200 %q", tc.q, code, strings.Join(names, ","), tc.want)
		}
	}

	// The listing route searches the same text.
	code, b := e.do(tok, http.MethodGet, "/Items?searchTerm=Ultimate&Recursive=true&Limit=10", nil)
	if code != 200 {
		t.Fatalf("/Items = %d", code)
	}
	var page struct {
		Items            []struct{ Name string }
		TotalRecordCount int
	}
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalRecordCount != 1 || len(page.Items) != 1 || page.Items[0].Name != "Mega Maquinas" {
		t.Errorf("/Items?searchTerm=Ultimate = %d %v, want the one movie", page.TotalRecordCount, page.Items)
	}
}
