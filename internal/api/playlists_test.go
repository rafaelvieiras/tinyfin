package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// plEnv is a server with an administrator, two ordinary users and three
// movies, for the playlist, search and browse tests.
type plEnv struct {
	t     *testing.T
	base  string
	st    storage.Store
	admin string // tokens
	alice string
	bob   string
	aID   string // user ids
	bID   string
	m     map[string]string // movie name -> id
}

func newPlEnv(t *testing.T) *plEnv {
	t.Helper()
	ts, st := newTestServer(t)
	adm := createTestAdminUser(t, st, "root", "rootpass")
	al := createTestUser(t, st, "alice", "alicepass")
	bo := createTestUser(t, st, "bob", "bobpass")
	lib := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	e := &plEnv{t: t, base: ts.URL, st: st, m: map[string]string{}, aID: string(al.ID), bID: string(bo.ID)}
	_ = adm
	e.admin = loginToken(t, ts.URL, "root", "rootpass")
	e.alice = loginToken(t, ts.URL, "alice", "alicepass")
	e.bob = loginToken(t, ts.URL, "bob", "bobpass")
	ticks := int64(600000000)
	for _, mv := range []struct {
		name    string
		year    int
		genres  []string
		studios []string
		people  []storage.Person
		rating  string
	}{
		{"Alpha", 2015, []string{"Drama"}, []string{"Acme"}, []storage.Person{{Name: "Ann Actor", Type: "Actor"}, {Name: "Dan Director", Type: "Director"}}, "PG"},
		{"Bravo", 2020, []string{"Action", "Drama"}, []string{"Acme", "Bolt"}, []storage.Person{{Name: "Ann Actor", Type: "Actor"}}, "R"},
		{"Charlie", 2018, nil, nil, nil, ""},
	} {
		y := mv.year
		it := storage.Item{
			Type: storage.ItemTypeMovie, LibraryID: lib, Name: mv.name, SortName: mv.name, ProductionYear: &y,
			Genres: mv.genres, Studios: mv.studios, People: mv.people, OfficialRating: mv.rating, RunTimeTicks: &ticks,
			Path: "/m/" + mv.name + ".mp4",
		}
		if err := st.UpsertItem(context.Background(), it); err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		e.m[it.Name] = string(it.ID)
	}
	return e
}

func (e *plEnv) do(tok, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+tok+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *plEnv) createPlaylist(tok string, body map[string]any) string {
	e.t.Helper()
	code, b := e.do(tok, http.MethodPost, "/Playlists", body)
	if code != 200 {
		e.t.Fatalf("create = %d %s", code, b)
	}
	var out struct{ Id string }
	if err := json.Unmarshal(b, &out); err != nil || out.Id == "" {
		e.t.Fatalf("create body %s", b)
	}
	return out.Id
}

func (e *plEnv) entries(tok, id string) []string {
	e.t.Helper()
	code, b := e.do(tok, http.MethodGet, "/Playlists/"+id, nil)
	if code != 200 {
		e.t.Fatalf("GET /Playlists/%s = %d %s", id, code, b)
	}
	var out struct{ ItemIds []string }
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatal(err)
	}
	return out.ItemIds
}

func (e *plEnv) names(tok, path string) []string {
	e.t.Helper()
	code, b := e.do(tok, http.MethodGet, path, nil)
	if code != 200 {
		e.t.Fatalf("GET %s = %d %s", path, code, b)
	}
	var out struct {
		Items []struct{ Name string }
	}
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatal(err)
	}
	var ns []string
	for _, it := range out.Items {
		ns = append(ns, it.Name)
	}
	return ns
}

func TestPlaylistEntriesAndOrder(t *testing.T) {
	e := newPlEnv(t)
	a, b, c := e.m["Alpha"], e.m["Bravo"], e.m["Charlie"]
	id := e.createPlaylist(e.admin, map[string]any{"Name": "Mix", "Ids": []string{a, b, a}})

	if got := strings.Join(e.entries(e.admin, id), ","); got != strings.Join([]string{a, b, a}, ",") {
		t.Fatalf("entries = %s", got)
	}
	if code, _ := e.do(e.admin, http.MethodPost, "/Playlists/"+id+"/Items?ids="+c+"&position=1", nil); code != 204 {
		t.Fatalf("add = %d", code)
	}
	if got := e.entries(e.admin, id); strings.Join(got, ",") != strings.Join([]string{a, c, b, a}, ",") {
		t.Fatalf("after add = %v", got)
	}
	if code, _ := e.do(e.admin, http.MethodPost, "/Playlists/"+id+"/Items/"+a+"/Move/3", nil); code != 204 {
		t.Fatalf("move = %d", code)
	}
	if got := e.entries(e.admin, id); strings.Join(got, ",") != strings.Join([]string{c, b, a, a}, ",") {
		t.Fatalf("after move = %v", got)
	}
	if code, _ := e.do(e.admin, http.MethodPost, "/Playlists/"+id+"/Items/"+a+"/Move/9", nil); code != 400 {
		t.Fatalf("move out of range = %d, want 400", code)
	}
	if code, _ := e.do(e.admin, http.MethodDelete, "/Playlists/"+id+"/Items?entryIds="+a, nil); code != 204 {
		t.Fatalf("remove = %d", code)
	}
	if got := e.entries(e.admin, id); strings.Join(got, ",") != strings.Join([]string{c, b}, ",") {
		t.Fatalf("after remove = %v", got)
	}
	// The item: counts, running time, metadata derived from the entries.
	code, body := e.do(e.admin, http.MethodGet, "/Items/"+id+"?Fields=Genres", nil)
	if code != 200 {
		t.Fatalf("item = %d", code)
	}
	var it struct {
		Type, MediaType string
		IsFolder        bool
		ChildCount      int
		RunTimeTicks    int64
		OfficialRating  string
		Genres          []string
	}
	if err := json.Unmarshal(body, &it); err != nil {
		t.Fatal(err)
	}
	if it.Type != "Playlist" || it.MediaType != "Video" || !it.IsFolder || it.ChildCount != 2 || it.RunTimeTicks != 1200000000 {
		t.Fatalf("item = %+v", it)
	}
	if it.OfficialRating != "R" || strings.Join(it.Genres, ",") != "Action,Drama" {
		t.Fatalf("derived metadata = %+v", it)
	}
}

func TestPlaylistAccessControl(t *testing.T) {
	e := newPlEnv(t)
	a := e.m["Alpha"]
	priv := e.createPlaylist(e.admin, map[string]any{"Name": "Private", "Ids": []string{a}, "IsPublic": false})
	open := e.createPlaylist(e.admin, map[string]any{"Name": "Open", "Ids": []string{a}, "IsPublic": true})
	shared := e.createPlaylist(e.admin, map[string]any{"Name": "Shared", "Ids": []string{a}, "IsPublic": false,
		"Users": []map[string]any{{"UserId": e.aID, "CanEdit": true}}})

	for _, tc := range []struct {
		name, tok, method, path string
		body                    any
		want                    int
	}{
		{"private is hidden", e.alice, "GET", "/Playlists/" + priv, nil, 404},
		{"private entries are hidden", e.alice, "GET", "/Playlists/" + priv + "/Items", nil, 404},
		{"private item is hidden", e.alice, "GET", "/Items/" + priv, nil, 404},
		{"open is readable", e.bob, "GET", "/Playlists/" + open, nil, 200},
		{"open is not editable", e.bob, "POST", "/Playlists/" + open + "/Items?ids=" + a, nil, 403},
		{"open cannot be renamed", e.bob, "POST", "/Playlists/" + open, map[string]any{"IsPublic": false}, 403},
		{"shared is readable by the share", e.alice, "GET", "/Playlists/" + shared, nil, 200},
		{"shared is editable by the share", e.alice, "POST", "/Playlists/" + shared + "/Items?ids=" + a, nil, 204},
		{"shared is hidden from others", e.bob, "GET", "/Playlists/" + shared, nil, 404},
		{"only the owner lists shares", e.alice, "GET", "/Playlists/" + shared + "/Users", nil, 403},
		{"owner lists shares", e.admin, "GET", "/Playlists/" + shared + "/Users", nil, 200},
		{"a reader cannot delete", e.bob, "DELETE", "/Items/" + open, nil, 401},
		{"a stranger cannot delete a hidden one", e.bob, "DELETE", "/Items/" + priv, nil, 404},
		{"anonymous", "", "GET", "/Playlists/" + open, nil, 401},
		{"other owner needs admin", e.alice, "POST", "/Playlists", map[string]any{"Name": "X", "UserId": "root"}, 400},
	} {
		code, b := e.do(tc.tok, tc.method, tc.path, tc.body)
		if code != tc.want {
			t.Errorf("%s: %s %s = %d (%s), want %d", tc.name, tc.method, tc.path, code, b, tc.want)
		}
	}
	// Listings show only what the user may read; the owner reads all three.
	if got := strings.Join(e.names(e.bob, "/Items?IncludeItemTypes=Playlist&Recursive=true"), ","); got != "Open" {
		t.Errorf("bob lists %q, want Open", got)
	}
	if got := strings.Join(e.names(e.alice, "/Items?IncludeItemTypes=Playlist&Recursive=true"), ","); got != "Open,Shared" {
		t.Errorf("alice lists %q, want Open,Shared", got)
	}
	if got := strings.Join(e.names(e.admin, "/Items?IncludeItemTypes=Playlist&Recursive=true"), ","); got != "Open,Private,Shared" {
		t.Errorf("admin lists %q, want Open,Private,Shared", got)
	}
	// The owner deletes; the items it linked stay.
	if code, _ := e.do(e.admin, http.MethodDelete, "/Items/"+priv, nil); code != 204 {
		t.Fatalf("owner delete = %d", code)
	}
	if code, _ := e.do(e.admin, http.MethodGet, "/Items/"+a, nil); code != 200 {
		t.Fatalf("movie after delete = %d", code)
	}
}

func TestPlaylistsView(t *testing.T) {
	e := newPlEnv(t)
	if got := e.names(e.bob, "/UserViews"); strings.Join(got, ",") != "Movies" {
		t.Fatalf("views before = %v", got)
	}
	id := e.createPlaylist(e.admin, map[string]any{"Name": "Mine", "Ids": []string{e.m["Alpha"]}, "IsPublic": true})
	if got := e.names(e.bob, "/UserViews"); strings.Join(got, ",") != "Movies,Playlists" {
		t.Fatalf("views after = %v", got)
	}
	_, b := e.do(e.bob, http.MethodGet, "/UserViews", nil)
	var views struct {
		Items []struct{ Id, Name, Type, CollectionType string }
	}
	if err := json.Unmarshal(b, &views); err != nil {
		t.Fatal(err)
	}
	var viewID string
	for _, v := range views.Items {
		if v.Name == "Playlists" {
			if v.Type != "UserView" || v.CollectionType != "playlists" {
				t.Fatalf("view = %+v", v)
			}
			viewID = v.Id
		}
	}
	if got := e.names(e.bob, "/Items?ParentId="+viewID); strings.Join(got, ",") != "Mine" {
		t.Fatalf("view lists %v", got)
	}
	// Its parent is the Playlists folder, which a recursive listing of
	// PlaylistsFolder finds once a playlist exists.
	if got := e.names(e.admin, "/Items?Recursive=true&IncludeItemTypes=PlaylistsFolder"); strings.Join(got, ",") != "Playlists" {
		t.Fatalf("folders = %v", got)
	}
	_ = id
}
