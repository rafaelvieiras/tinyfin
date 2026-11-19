package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// newLibraryOrderEnv serves a server with the libraries given, created in that
// order, and returns the base URL, the admin token and one token per extra
// user name.
func newLibraryOrderEnv(t *testing.T, names []string) (string, string, map[string]string) {
	t.Helper()
	ts, st := newTestServer(t)
	root := t.TempDir()
	for i, name := range names {
		lib := storage.Library{
			ID:             storage.LibraryID(fmt.Sprintf("%032x", i+1)),
			Name:           name,
			CollectionType: storage.CollectionTypeMovies,
			Paths:          []string{root},
		}
		if err := st.UpsertLibrary(context.Background(), lib); err != nil {
			t.Fatalf("UpsertLibrary(%q): %v", name, err)
		}
	}
	createTestAdminUser(t, st, "root", "rootpass")
	createTestUser(t, st, "viewer", "viewerpass")
	return ts.URL, loginToken(t, ts.URL, "root", "rootpass"), map[string]string{
		"viewer": loginToken(t, ts.URL, "viewer", "viewerpass"),
	}
}

// libraryNames reads one library list and returns the names in the order the
// server sent them. "$.Items[*].Name" is the query-result shape; a bare array
// (GET /Library/VirtualFolders) is handled too.
func libraryNames(t *testing.T, base, token, path string) (names []string, status int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, resp.StatusCode
	}
	var asObject struct {
		Items []struct{ Name string } `json:"Items"`
	}
	if err := json.Unmarshal(body, &asObject); err == nil && len(asObject.Items) > 0 {
		for _, it := range asObject.Items {
			names = append(names, it.Name)
		}
		return names, resp.StatusCode
	}
	var asArray []struct{ Name string }
	if err := json.Unmarshal(body, &asArray); err != nil {
		// An error body (a problem document) has no names to report; the
		// status is what the caller is checking.
		return nil, resp.StatusCode
	}
	for _, it := range asArray {
		names = append(names, it.Name)
	}
	return names, resp.StatusCode
}

// TestLibraryListsAreOrderedBySortName covers admin.libraries.R4: the lists are
// ordered by the sort name, not by the bytes of the name. A byte comparison
// puts "10 Coisas" before "2 Coisas", every capital before every small letter
// and every accented name after "z", which is what a `ORDER BY name` gives and
// what the reference server does not do.
func TestLibraryListsAreOrderedBySortName(t *testing.T) {
	created := []string{"Zebra", "apple", "Ábaco", "10 Coisas", "Banana", "2 Coisas"}
	base, adminTok, _ := newLibraryOrderEnv(t, created)
	want := []string{"2 Coisas", "10 Coisas", "Ábaco", "apple", "Banana", "Zebra"}

	for _, path := range []string{"/UserViews", "/Library/MediaFolders?isHidden=false"} {
		names, status := libraryNames(t, base, adminTok, path)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", path, status)
		}
		if got, want := strings.Join(names, "|"), strings.Join(want, "|"); got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}

	// The root form of GET /Items carries the same libraries and then the
	// Playlists folder, which stays last (items.list.R4).
	names, status := libraryNames(t, base, adminTok, "/Items")
	if status != http.StatusOK {
		t.Fatalf("/Items: status %d", status)
	}
	if got, want := strings.Join(names, "|"), strings.Join(want, "|")+"|Playlists"; got != want {
		t.Errorf("/Items = %s, want %s", got, want)
	}

	// GET /Library/MediaFolders puts the playlists folder in its own place.
	names, _ = libraryNames(t, base, adminTok, "/Library/MediaFolders")
	if got, want := strings.Join(names, "|"), "2 Coisas|10 Coisas|Ábaco|apple|Banana|Playlists|Zebra"; got != want {
		t.Errorf("/Library/MediaFolders = %s, want %s", got, want)
	}
}

// TestVirtualFoldersListNewestFirst covers admin.libraries.R5: the admin list
// is the reverse of the creation order, not sorted by name.
func TestVirtualFoldersListNewestFirst(t *testing.T) {
	base, adminTok, _ := newLibraryOrderEnv(t, []string{"Zebra", "apple", "Ábaco", "10 Coisas", "Banana", "2 Coisas"})
	names, status := libraryNames(t, base, adminTok, "/Library/VirtualFolders")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	want := "2 Coisas|Banana|10 Coisas|Ábaco|apple|Zebra"
	if got := strings.Join(names, "|"); got != want {
		t.Errorf("/Library/VirtualFolders = %s, want %s (newest first)", got, want)
	}
}

// TestUserViewsAsksForAUser covers items.views.R1 and R2: who may ask for whose
// views, and the one difference between the two routes — an identifier that
// names nobody is a 404 on the legacy path and a 403 here.
func TestUserViewsAsksForAUser(t *testing.T) {
	base, adminTok, tokens := newLibraryOrderEnv(t, []string{"Movies", "Shows"})
	viewer := tokens["viewer"]

	adminID := ""
	viewerID := ""
	req, _ := http.NewRequest(http.MethodGet, base+"/Users", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+adminTok+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list []struct{ Id, Name string }
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	for _, u := range list {
		switch u.Name {
		case "root":
			adminID = u.Id
		case "viewer":
			viewerID = u.Id
		}
	}
	if adminID == "" || viewerID == "" {
		t.Fatalf("admin id %q, viewer id %q", adminID, viewerID)
	}

	cases := []struct {
		name   string
		token  string
		path   string
		status int
	}{
		{"own views, no parameter", viewer, "/UserViews", 200},
		{"own views, named", viewer, "/UserViews?userId=" + viewerID, 200},
		{"another user's views", viewer, "/UserViews?userId=" + adminID, 403},
		{"an identifier that names nobody", viewer, "/UserViews?userId=0123456789abcdef0123456789abcdef", 403},
		{"an administrator asks for another user", adminTok, "/UserViews?userId=" + viewerID, 200},
		{"the legacy path for the caller", viewer, "/Users/" + viewerID + "/Views", 200},
		{"the legacy path for another user", viewer, "/Users/" + adminID + "/Views", 403},
		{"the legacy path, administrator", adminTok, "/Users/" + viewerID + "/Views", 200},
		{"the legacy path, nobody by that id", adminTok, "/Users/0123456789abcdef0123456789abcdef/Views", 404},
		{"the legacy path, malformed id", adminTok, "/Users/not-an-id/Views", 400},
		{"the legacy path, anonymous", "", "/Users/" + viewerID + "/Views", 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, status := libraryNames(t, base, tc.token, tc.path)
			if status != tc.status {
				t.Errorf("%s = %d, want %d", tc.path, status, tc.status)
			}
		})
	}
}
