package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/collection"
	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

type collEnv struct {
	t        *testing.T
	base     string
	st       storage.Store
	dataDir  string
	lib      storage.Library
	adminTok string
	ids      map[string]string // movie name -> item id
}

// writeMovie writes <root>/<title> (<year>)/<title> (<year>).mp4 and .nfo.
func writeMovie(t *testing.T, root, title string, year int, nfoBody string) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%s (%d)", title, year))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, fmt.Sprintf("%s (%d)", title, year))
	if err := os.WriteFile(base+".mp4", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nfo := fmt.Sprintf("<movie><title>%s</title><year>%d</year>%s</movie>", title, year, nfoBody)
	if err := os.WriteFile(base+".nfo", []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *collEnv) do(tok, method, path string) (int, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.base+path, nil)
	if err != nil {
		e.t.Fatal(err)
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

func (e *collEnv) items(tok, path string) (names []string, total int) {
	e.t.Helper()
	code, b := e.do(tok, http.MethodGet, path)
	if code != http.StatusOK {
		e.t.Fatalf("GET %s = %d %s", path, code, b)
	}
	var out struct {
		Items            []struct{ Name, Type string }
		TotalRecordCount int
	}
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatal(err)
	}
	for _, it := range out.Items {
		names = append(names, it.Name)
	}
	return names, out.TotalRecordCount
}

func (e *collEnv) create(tok, name string, ids ...string) (int, string) {
	e.t.Helper()
	q := url.Values{"Name": {name}}
	if len(ids) > 0 {
		q.Set("Ids", strings.Join(ids, ","))
	}
	code, b := e.do(tok, http.MethodPost, "/Collections?"+q.Encode())
	if code != http.StatusOK {
		return code, ""
	}
	var out struct{ Id string }
	if err := json.Unmarshal(b, &out); err != nil || out.Id == "" {
		e.t.Fatalf("create %q: %s", name, b)
	}
	return code, out.Id
}

// newCollEnv serves a movies library scanned from the given movies
// (title, year, extra nfo) with the automatic-collections option set as asked.
func newCollEnv(t *testing.T, auto bool, movies []struct {
	Title string
	Year  int
	NFO   string
}) *collEnv {
	t.Helper()
	ts, st := newTestServer(t)
	root := t.TempDir()
	for _, m := range movies {
		writeMovie(t, root, m.Title, m.Year, m.NFO)
	}
	opts, _ := json.Marshal(map[string]any{"AutomaticallyAddToCollection": auto})
	lib := storage.Library{ID: "0123456789abcdef0123456789abcdef", Name: "Movies", CollectionType: storage.CollectionTypeMovies,
		Paths: []string{root}, LibraryOptions: opts}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if _, err := scan.New(st, metadata.NoopProvider{}, scan.Config{DataDir: dataDir}).ScanLibrary(context.Background(), lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	createTestAdminUser(t, st, "root", "rootpass")
	e := &collEnv{t: t, base: ts.URL, st: st, dataDir: dataDir, lib: lib, adminTok: loginToken(t, ts.URL, "root", "rootpass"), ids: map[string]string{}}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeMovie}, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		e.ids[it.Name] = string(it.ID)
	}
	return e
}

type mv = struct {
	Title string
	Year  int
	NFO   string
}

func TestCollectionsLifecycleAndPermissions(t *testing.T) {
	e := newCollEnv(t, false, []mv{
		{"Alpha", 2015, "<genre>Drama</genre><mpaa>R</mpaa>"},
		{"Bravo", 2020, "<genre>Action</genre><mpaa>PG</mpaa>"},
		{"Charlie", 2018, ""},
	})
	a, b, c := e.ids["Alpha"], e.ids["Bravo"], e.ids["Charlie"]

	// Creating answers an id; members are listed in year order.
	code, id := e.create(e.adminTok, "The Box", a, b, a)
	if code != 200 {
		t.Fatalf("create = %d", code)
	}
	if names, total := e.items(e.adminTok, "/Items?ParentId="+id); total != 2 || strings.Join(names, ",") != "Alpha,Bravo" {
		t.Fatalf("members = %v (%d)", names, total)
	}
	// Same name: same collection, members replaced.
	if code, id2 := e.create(e.adminTok, "The Box", c); code != 200 || id2 != id {
		t.Fatalf("re-create = %d %s, want same id %s", code, id2, id)
	}
	if names, _ := e.items(e.adminTok, "/Items?ParentId="+id); strings.Join(names, ",") != "Charlie" {
		t.Fatalf("members after re-create = %v", names)
	}
	// Add keeps a set, remove ignores strangers.
	if code, _ := e.do(e.adminTok, http.MethodPost, "/Collections/"+id+"/Items?Ids="+a+","+b+","+a); code != 204 {
		t.Fatalf("add = %d", code)
	}
	if code, _ := e.do(e.adminTok, http.MethodPost, "/Collections/"+id+"/Items?Ids="+a); code != 204 {
		t.Fatalf("add again = %d", code)
	}
	names, total := e.items(e.adminTok, "/Items?ParentId="+id)
	if total != 3 || strings.Join(names, ",") != "Alpha,Charlie,Bravo" {
		t.Fatalf("members = %v (%d), want Alpha,Charlie,Bravo", names, total)
	}
	if code, _ := e.do(e.adminTok, http.MethodDelete, "/Collections/"+id+"/Items?Ids="+b+",00000000000000000000000000000001"); code != 204 {
		t.Fatalf("remove = %d", code)
	}
	if _, total := e.items(e.adminTok, "/Items?ParentId="+id); total != 2 {
		t.Fatalf("members after remove = %d", total)
	}
	// Recursive listing of the library with the BoxSet type finds it; the
	// collections library is a view now.
	if names, _ := e.items(e.adminTok, "/Items?ParentId="+string(e.lib.ID)+"&IncludeItemTypes=BoxSet&Recursive=true"); strings.Join(names, ",") != "The Box" {
		t.Fatalf("boxes of the library = %v", names)
	}
	if names, _ := e.items(e.adminTok, "/UserViews"); strings.Join(names, ",") != "Collections,Movies" {
		t.Fatalf("views = %v", names)
	}

	// Errors.
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"unknown id", http.MethodPost, "/Collections?Name=X&Ids=00000000000000000000000000000001", 400},
		{"no name", http.MethodPost, "/Collections?Ids=" + a, 400},
		{"blank name", http.MethodPost, "/Collections?Name=%20&Ids=" + a, 400},
		{"not a guid", http.MethodPost, "/Collections?Name=X&Ids=nope", 400},
		{"add to a movie", http.MethodPost, "/Collections/" + a + "/Items?Ids=" + b, 400},
		{"add unknown", http.MethodPost, "/Collections/" + id + "/Items?Ids=00000000000000000000000000000001", 400},
		{"add no ids", http.MethodPost, "/Collections/" + id + "/Items", 204},
		{"collection not a guid", http.MethodDelete, "/Collections/zzz/Items?Ids=" + a, 400},
		{"delete a movie", http.MethodDelete, "/Items/" + a, 403},
	} {
		if code, body := e.do(e.adminTok, tc.method, tc.path); code != tc.want {
			t.Errorf("%s: %s %s = %d (%s), want %d", tc.name, tc.method, tc.path, code, body, tc.want)
		}
	}
	if names, _ := e.items(e.adminTok, "/Items?IncludeItemTypes=BoxSet&Recursive=true"); strings.Join(names, ",") != "The Box" {
		t.Errorf("rejected requests created something: %v", names)
	}

	// Permissions.
	createTestUser(t, e.st, "plain", "plainpass")
	mgr := createTestUser(t, e.st, "mgr", "mgrpass")
	mgr.Policy = `{"EnableCollectionManagement":true}`
	if err := e.st.UpdateUser(context.Background(), mgr); err != nil {
		t.Fatal(err)
	}
	plainTok := loginToken(t, e.base, "plain", "plainpass")
	mgrTok := loginToken(t, e.base, "mgr", "mgrpass")
	if code, _ := e.do("", http.MethodPost, "/Collections?Name=Anon"); code != 401 {
		t.Errorf("anonymous create = %d", code)
	}
	if code, _ := e.create(plainTok, "Plain", a); code != 403 {
		t.Errorf("plain create = %d, want 403", code)
	}
	if code, _ := e.do(plainTok, http.MethodPost, "/Collections/"+id+"/Items?Ids="+b); code != 403 {
		t.Errorf("plain add = %d, want 403", code)
	}
	if code, _ := e.do(plainTok, http.MethodDelete, "/Items/"+id); code != 401 {
		t.Errorf("plain delete = %d, want 401", code)
	}
	code, mid := e.create(mgrTok, "Managed", a)
	if code != 200 {
		t.Fatalf("manager create = %d", code)
	}
	if code, _ := e.do(mgrTok, http.MethodDelete, "/Items/"+mid); code != 204 {
		t.Errorf("manager delete = %d, want 204", code)
	}
	if code, _ := e.do(e.adminTok, http.MethodGet, "/Items/"+mid); code != 404 {
		t.Errorf("deleted collection = %d, want 404", code)
	}
	if code, _ := e.do(e.adminTok, http.MethodGet, "/Items/"+a); code != 200 {
		t.Errorf("member after the collection was deleted = %d", code)
	}
}

func TestCollectionDerivedMetadataAndRefresh(t *testing.T) {
	e := newCollEnv(t, false, []mv{
		{"Alpha", 2001, "<genre>Drama</genre><mpaa>PG</mpaa>"},
		{"Bravo", 2002, "<genre>Action</genre><mpaa>R</mpaa>"},
		{"Charlie", 2003, ""},
	})
	meta := func(id string) (year int, rating string, genres []string) {
		t.Helper()
		item, err := e.st.GetItem(context.Background(), storage.ItemID(id))
		if err != nil {
			t.Fatal(err)
		}
		if item.ProductionYear != nil {
			year = *item.ProductionYear
		}
		return year, item.OfficialRating, item.Genres
	}
	_, id := e.create(e.adminTok, "Derived", e.ids["Bravo"], e.ids["Alpha"], e.ids["Charlie"])
	if y, r, g := meta(id); y != 2001 || r != "PG" || strings.Join(g, ",") != "Action,Drama" {
		t.Fatalf("created: year %d rating %q genres %v", y, r, g)
	}
	_, id2 := e.create(e.adminTok, "Grows", e.ids["Charlie"])
	if y, r, g := meta(id2); y != 2003 || r != "" || len(g) != 0 {
		t.Fatalf("plain: year %d rating %q genres %v", y, r, g)
	}
	e.do(e.adminTok, http.MethodPost, "/Collections/"+id2+"/Items?Ids="+e.ids["Bravo"])
	if y, r, g := meta(id2); y != 2003 || r != "R" || strings.Join(g, ",") != "Action" {
		t.Fatalf("after adding Bravo: year %d rating %q genres %v", y, r, g)
	}
	e.do(e.adminTok, http.MethodPost, "/Collections/"+id2+"/Items?Ids="+e.ids["Alpha"])
	if y, r, g := meta(id2); y != 2003 || r != "PG" || strings.Join(g, ",") != "Action" {
		t.Fatalf("after adding Alpha: year %d rating %q genres %v", y, r, g)
	}
	e.do(e.adminTok, http.MethodDelete, "/Collections/"+id2+"/Items?Ids="+e.ids["Alpha"])
	if _, r, _ := meta(id2); r != "PG" {
		t.Fatalf("removal changed the rating to %q", r)
	}
	if _, err := collection.Refresh(context.Background(), e.st, storage.ItemID(id2)); err != nil {
		t.Fatal(err)
	}
	if y, r, g := meta(id2); y != 2002 || r != "R" || strings.Join(g, ",") != "Action" {
		t.Fatalf("after refresh: year %d rating %q genres %v", y, r, g)
	}
}

func TestAutomaticCollectionsFromScan(t *testing.T) {
	movies := []mv{
		{"One", 2001, "<set><name>Saga</name></set>"},
		{"Two", 2002, "<set>Saga</set>"},
		{"Three", 2003, "<set><name>Saga</name></set>"},
		{"Alone", 2004, "<set><name>Single</name></set>"},
		{"Up", 2005, "<set><name>Case</name></set>"},
		{"Low", 2006, "<set><name>case</name></set>"},
		{"PadA", 2007, "<set><name>  Pad  </name></set>"},
		{"PadB", 2008, "<set><name>  Pad  </name></set>"},
		{"Plain", 2009, ""},
	}
	on := newCollEnv(t, true, movies)
	boxes, _ := on.items(on.adminTok, "/Items?IncludeItemTypes=BoxSet&Recursive=true")
	if strings.Join(boxes, ",") != "Pad,Saga" {
		t.Fatalf("boxes = %v, want Pad and Saga only", boxes)
	}
	var saga string
	code, b := on.do(on.adminTok, http.MethodGet, "/Items?IncludeItemTypes=BoxSet&Recursive=true")
	if code != 200 {
		t.Fatal(code)
	}
	var list struct{ Items []struct{ Id, Name string } }
	_ = json.Unmarshal(b, &list)
	for _, it := range list.Items {
		if it.Name == "Saga" {
			saga = it.Id
		}
	}
	if names, _ := on.items(on.adminTok, "/Items?ParentId="+saga); strings.Join(names, ",") != "One,Two,Three" {
		t.Fatalf("Saga members = %v", names)
	}

	// A second scan changes nothing; an incremental one keeps the links.
	sc := scan.New(on.st, metadata.NoopProvider{}, scan.Config{DataDir: on.dataDir})
	for i := 0; i < 2; i++ {
		if _, err := sc.ScanLibrary(context.Background(), on.lib); err != nil {
			t.Fatal(err)
		}
	}
	if names, total := on.items(on.adminTok, "/Items?ParentId="+saga); total != 3 || len(names) != 3 {
		t.Fatalf("Saga members after rescans = %v", names)
	}
	// A member removed by hand comes back at the next full scan, one added by
	// hand stays.
	on.do(on.adminTok, http.MethodDelete, "/Collections/"+saga+"/Items?Ids="+on.ids["One"])
	on.do(on.adminTok, http.MethodPost, "/Collections/"+saga+"/Items?Ids="+on.ids["Plain"])
	if _, err := sc.ScanLibrary(context.Background(), on.lib); err != nil {
		t.Fatal(err)
	}
	if names, _ := on.items(on.adminTok, "/Items?ParentId="+saga); strings.Join(names, ",") != "One,Two,Three,Plain" {
		t.Fatalf("Saga members after the full scan = %v", names)
	}
	// A scan that skips collections does not repair a deleted one.
	on.do(on.adminTok, http.MethodDelete, "/Items/"+saga)
	if _, err := sc.ScanLibrary(scan.WithoutCollections(context.Background()), on.lib); err != nil {
		t.Fatal(err)
	}
	if boxes, _ := on.items(on.adminTok, "/Items?IncludeItemTypes=BoxSet&Recursive=true"); strings.Join(boxes, ",") != "Pad" {
		t.Fatalf("boxes after a collection-less scan = %v", boxes)
	}
	if _, err := sc.ScanLibrary(context.Background(), on.lib); err != nil {
		t.Fatal(err)
	}
	if boxes, _ := on.items(on.adminTok, "/Items?IncludeItemTypes=BoxSet&Recursive=true"); strings.Join(boxes, ",") != "Pad,Saga" {
		t.Fatalf("boxes after the full scan = %v", boxes)
	}

	off := newCollEnv(t, false, movies)
	if boxes, _ := off.items(off.adminTok, "/Items?IncludeItemTypes=BoxSet&Recursive=true"); len(boxes) != 0 {
		t.Fatalf("option off still built %v", boxes)
	}
}

func TestCollectionVisibilityAndCollapse(t *testing.T) {
	e := newCollEnv(t, false, []mv{{"Alpha", 2015, ""}, {"Bravo", 2020, ""}, {"Charlie", 2018, ""}})
	_, box := e.create(e.adminTok, "Visible", e.ids["Alpha"], e.ids["Bravo"])
	_, empty := e.create(e.adminTok, "Empty")

	user := createTestUser(t, e.st, "viewer", "viewerpass")
	setFolders := func(ids ...string) string {
		pol, _ := json.Marshal(map[string]any{"EnableAllFolders": false, "EnabledFolders": ids})
		user.Policy = string(pol)
		if err := e.st.UpdateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		return loginToken(t, e.base, "viewer", "viewerpass")
	}
	// Only the collections library: the empty one is listed, the other has
	// no visible member.
	tok := setFolders(string(collection.LibraryID()))
	names, _ := e.items(tok, "/Items?IncludeItemTypes=BoxSet&Recursive=true")
	if strings.Join(names, ",") != "Empty" {
		t.Fatalf("collections-only user sees %v", names)
	}
	if code, _ := e.do(tok, http.MethodGet, "/Items?ParentId="+box); code != 401 {
		t.Errorf("opening a collection with no visible member = %d, want 401", code)
	}
	if code, _ := e.do(tok, http.MethodGet, "/Items/"+box); code != 404 {
		t.Errorf("reading it = %d, want 404", code)
	}
	if views, _ := e.items(tok, "/UserViews"); strings.Join(views, ",") != "Collections" {
		t.Errorf("views = %v", views)
	}
	// Only the movies library: the collections library is not a view and
	// nothing is listed, but an empty collection can still be read.
	tok = setFolders(string(e.lib.ID))
	if names, _ := e.items(tok, "/Items?IncludeItemTypes=BoxSet&Recursive=true"); len(names) != 0 {
		t.Errorf("movies-only user sees %v", names)
	}
	if views, _ := e.items(tok, "/UserViews"); strings.Join(views, ",") != "Movies" {
		t.Errorf("views = %v", views)
	}
	if code, _ := e.do(tok, http.MethodGet, "/Items/"+empty); code != 200 {
		t.Errorf("empty collection by id = %d", code)
	}
	// Both: everything, and the collapse replaces the members.
	tok = setFolders(string(e.lib.ID), string(collection.LibraryID()))
	names, _ = e.items(tok, "/Items?IncludeItemTypes=BoxSet&Recursive=true")
	if strings.Join(names, ",") != "Empty,Visible" {
		t.Errorf("sees %v", names)
	}
	names, _ = e.items(tok, "/Items?ParentId="+string(e.lib.ID)+"&IncludeItemTypes=Movie&Recursive=true&CollapseBoxSetItems=true")
	if strings.Join(names, ",") != "Charlie,Visible" {
		t.Errorf("collapsed = %v", names)
	}
	names, _ = e.items(tok, "/Items?ParentId="+string(e.lib.ID)+"&IncludeItemTypes=Movie&Recursive=true")
	if strings.Join(names, ",") != "Alpha,Bravo,Charlie" {
		t.Errorf("not collapsed = %v", names)
	}
	// Latest leaves collections out; counts have them.
	code, b := e.do(tok, http.MethodGet, "/Items/Latest?Limit=50")
	if code != 200 || strings.Contains(string(b), `"BoxSet"`) {
		t.Errorf("latest = %d %s", code, b)
	}
	code, b = e.do(tok, http.MethodGet, "/Items/Counts")
	var counts itemCounts
	_ = json.Unmarshal(b, &counts)
	if code != 200 || counts.BoxSetCount != 2 {
		t.Errorf("counts = %d %+v", code, counts)
	}
}

// Played marks on a collection are written to the leaves below it.
func TestCollectionPlayedMarksLeaves(t *testing.T) {
	e := newCollEnv(t, false, []mv{{"Alpha", 2015, ""}, {"Bravo", 2020, ""}})
	_, box := e.create(e.adminTok, "Played", e.ids["Alpha"], e.ids["Bravo"])
	played := func(id string) (bool, int) {
		code, b := e.do(e.adminTok, http.MethodGet, "/Items/"+id)
		if code != 200 {
			t.Fatal(code)
		}
		var d struct {
			UserData struct {
				Played            bool
				PlayCount         int
				UnplayedItemCount *int
				PlayedPercentage  *float64
			}
		}
		_ = json.Unmarshal(b, &d)
		return d.UserData.Played, d.UserData.PlayCount
	}
	if code, _ := e.do(e.adminTok, http.MethodPost, "/UserPlayedItems/"+e.ids["Alpha"]); code != 200 {
		t.Fatal(code)
	}
	if p, _ := played(box); p {
		t.Error("half-played collection reports played")
	}
	if code, b := e.do(e.adminTok, http.MethodPost, "/UserPlayedItems/"+box); code != 200 || !strings.Contains(string(b), `"Played":true`) {
		t.Fatalf("mark played = %d %s", code, b)
	}
	if p, n := played(e.ids["Bravo"]); !p || n != 1 {
		t.Errorf("leaf after the mark: played %v count %d", p, n)
	}
	if code, _ := e.do(e.adminTok, http.MethodDelete, "/UserPlayedItems/"+box); code != 200 {
		t.Fatal(code)
	}
	if p, n := played(e.ids["Alpha"]); p || n != 0 {
		t.Errorf("leaf after the unmark: played %v count %d", p, n)
	}
}
