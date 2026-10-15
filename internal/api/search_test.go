package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (e *plEnv) hints(tok, q string) (names []string, total int, code int) {
	e.t.Helper()
	code, b := e.do(tok, http.MethodGet, "/Search/Hints?"+q, nil)
	if code != 200 {
		return nil, 0, code
	}
	var out struct {
		SearchHints []struct{ Name, Type string }
		TotalRecordCount int
	}
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatal(err)
	}
	for _, h := range out.SearchHints {
		names = append(names, h.Type+":"+h.Name)
	}
	return names, out.TotalRecordCount, 200
}

func TestSearchHints(t *testing.T) {
	e := newPlEnv(t)
	for _, tc := range []struct {
		q    string
		want string
		code int
	}{
		{"searchTerm=alpha", "Movie:Alpha", 200},
		{"searchTerm=%20ALPHA%20", "Movie:Alpha", 200},
		{"searchTerm=ann", "Person:Ann Actor", 200},
		{"searchTerm=ann&includePeople=false", "", 200},
		{"searchTerm=drama", "Genre:Drama", 200},
		{"searchTerm=acme&includeStudios=false", "", 200},
		{"searchTerm=acme", "Studio:Acme", 200},
		{"searchTerm=...", "", 200},
		{"searchTerm=alpha&mediaTypes=Audio", "", 200},
		{"searchTerm=alpha&isSeries=true", "", 200},
		{"searchTerm=alpha&parentId=" + url.QueryEscape("00000000000000000000000000000009"), "", 200},
		{"searchTerm=", "", 400},
		{"", "", 400},
		{"searchTerm=a&limit=x", "", 400},
	} {
		names, _, code := e.hints(e.alice, tc.q)
		if code != tc.code || strings.Join(names, ",") != tc.want {
			t.Errorf("%s: %d %v, want %d %q", tc.q, code, names, tc.code, tc.want)
		}
	}
	if code, _ := e.do("", http.MethodGet, "/Search/Hints?searchTerm=a", nil); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}

func TestBrowseNamedItems(t *testing.T) {
	e := newPlEnv(t)
	for _, tc := range []struct{ path, want string }{
		{"/Genres", "Action,Drama"},
		{"/Genres?sortOrder=Descending&sortBy=SortName", "Drama,Action"},
		{"/Genres?includeItemTypes=Series", ""},
		{"/Studios", "Acme,Bolt"},
		{"/Persons", "Ann Actor,Dan Director"},
		{"/Persons?personTypes=Director", "Dan Director"},
		{"/Years?sortBy=SortName", "2015,2018,2020"},
	} {
		if got := strings.Join(e.names(e.alice, tc.path), ","); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
	// The single forms: counts of what the caller sees.
	code, b := e.do(e.alice, http.MethodGet, "/Genres/DRAMA", nil)
	var g struct {
		Name       string
		ChildCount int
		MovieCount int
		Id         string
	}
	if err := json.Unmarshal(b, &g); err != nil || code != 200 || g.Name != "Drama" || g.ChildCount != 2 || g.MovieCount != 2 {
		t.Fatalf("genre = %d %s", code, b)
	}
	if code, _ := e.do(e.alice, http.MethodGet, "/Items/"+g.Id, nil); code != 200 {
		t.Errorf("genre by item id = %d", code)
	}
	if code, _ := e.do(e.alice, http.MethodGet, "/Persons/Nobody", nil); code != 404 {
		t.Errorf("unknown person = %d", code)
	}
	if code, _ := e.do(e.alice, http.MethodGet, "/Years/abc", nil); code != 400 {
		t.Errorf("year abc = %d", code)
	}
	// Similar: Alpha shares a genre and a person with Bravo, nothing with Charlie.
	if got := strings.Join(e.names(e.alice, "/Items/"+e.m["Alpha"]+"/Similar"), ","); got != "Bravo" {
		t.Errorf("similar = %q", got)
	}
	// Filters that name a name.
	for _, tc := range []struct{ q, want string }{
		{"Genres=action|DRAMA", "Alpha,Bravo"},
		{"Genres=Drama,Action", ""},
		{"Studios=bolt", "Bravo"},
		{"Person=Ann%20Actor", "Alpha,Bravo"},
		{"Person=ann%20actor", ""},
		{"Person=Ann%20Actor&PersonTypes=Director", ""},
	} {
		if got := strings.Join(e.names(e.alice, "/Items?Recursive=true&IncludeItemTypes=Movie&"+tc.q), ","); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.q, got, tc.want)
		}
	}
	// Facets.
	code, b = e.do(e.alice, http.MethodGet, "/Items/Filters2", nil)
	if code != 200 || !strings.Contains(string(b), `"Name":"Action"`) {
		t.Errorf("Filters2 = %d %s", code, b)
	}
}

func TestSmallRoutes(t *testing.T) {
	e := newPlEnv(t)
	for _, tc := range []struct {
		tok, method, path string
		want              int
	}{
		{"", "GET", "/System/Ping", 200},
		{"", "POST", "/System/Ping", 200},
		{"", "GET", "/GetUtcTime", 200},
		{"", "GET", "/Localization/Options", 401},
		{e.alice, "GET", "/Localization/Options", 200},
		{e.alice, "GET", "/Localization/ParentalRatings", 200},
		{e.alice, "GET", "/Items/" + "00000000000000000000000000000001" + "/Intros", 404},
		{e.alice, "GET", "/Items/Root", 200},
	} {
		if code, _ := e.do(tc.tok, tc.method, tc.path, nil); code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, code, tc.want)
		}
	}
}
