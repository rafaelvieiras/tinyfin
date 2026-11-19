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

// newGroupingEnv serves a server with one library of each collection type the
// fixture cares about, in a deliberately scrambled creation order, and returns
// the base URL and an admin token.
func newGroupingEnv(t *testing.T) (base, adminToken, adminID string, st storage.UserStore) {
	t.Helper()
	ts, store := newTestServer(t)
	root := t.TempDir()
	libs := []struct {
		key  string
		name string
		typ  storage.CollectionType
	}{
		{"music", "Music", storage.CollectionTypeMusic},
		{"shows", "Shows", storage.CollectionTypeTVShows},
		{"ten", "10 Coisas", storage.CollectionTypeMovies},
		{"home", "HomeVideos", storage.CollectionTypeHomeVideos},
		{"mixed", "Zeta-Mixed", storage.CollectionTypeMixed},
		{"nine", "9 Vidas", storage.CollectionTypeMovies},
	}
	for i, l := range libs {
		lib := storage.Library{ID: storage.LibraryID(fmt.Sprintf("%032x", i+11)), Name: l.name, CollectionType: l.typ, Paths: []string{root}}
		if err := store.UpsertLibrary(context.Background(), lib); err != nil {
			t.Fatalf("UpsertLibrary(%q): %v", l.name, err)
		}
	}
	admin := createTestAdminUser(t, store, "root", "rootpass")
	return ts.URL, loginToken(t, ts.URL, "root", "rootpass"), string(admin.ID), store
}

type groupingOption struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

func getGroupingOptions(t *testing.T, base, token, path string) (opts []groupingOption, status int) {
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
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	if err := json.NewDecoder(resp.Body).Decode(&opts); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return opts, resp.StatusCode
}

// TestGroupingOptionsFilterAndOrder is items.grouping.R1-R3: only the
// groupable collection types are offered, and they are ordered by name, which
// puts "10 Coisas" before "9 Vidas" (a sort-name order would reverse that).
func TestGroupingOptionsFilterAndOrder(t *testing.T) {
	base, token, adminID, _ := newGroupingEnv(t)

	for _, path := range []string{
		"/UserViews/GroupingOptions",
		"/Users/" + adminID + "/GroupingOptions",
	} {
		opts, status := getGroupingOptions(t, base, token, path)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", path, status)
		}
		var got []string
		for _, o := range opts {
			got = append(got, o.Name)
		}
		want := []string{"10 Coisas", "9 Vidas", "Shows", "Zeta-Mixed"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: names %v, want %v", path, got, want)
		}
	}
}

// TestGroupingOptionsFallsBackToTheCaller is items.grouping.R4: an identifier
// that names nobody is not an error and does not change the answer, while a
// value that is not an identifier is a validation error.
func TestGroupingOptionsFallsBackToTheCaller(t *testing.T) {
	base, token, _, _ := newGroupingEnv(t)
	own, _ := getGroupingOptions(t, base, token, "/UserViews/GroupingOptions")

	for _, path := range []string{
		"/UserViews/GroupingOptions?userId=00000000000000000000000000000000",
		"/Users/00000000000000000000000000000000/GroupingOptions",
	} {
		got, status := getGroupingOptions(t, base, token, path)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", path, status)
		}
		if len(got) != len(own) {
			t.Fatalf("%s: %d options, want the caller's %d", path, len(got), len(own))
		}
		for i := range got {
			if got[i] != own[i] {
				t.Errorf("%s: option %d is %+v, want %+v", path, i, got[i], own[i])
			}
		}
	}

	for _, path := range []string{
		"/UserViews/GroupingOptions?userId=nope",
		"/Users/nope/GroupingOptions",
	} {
		if _, status := getGroupingOptions(t, base, token, path); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, status)
		}
	}
}

// TestGroupingOptionsAuthorization is the authorization half of
// items.grouping.R4: another user's options are refused to a non-administrator
// and allowed to one, and an anonymous request is refused.
func TestGroupingOptionsAuthorization(t *testing.T) {
	base, adminToken, adminID, store := newGroupingEnv(t)
	createTestUser(t, store, "viewer", "viewerpass")
	viewerToken := loginToken(t, base, "viewer", "viewerpass")

	for _, path := range []string{
		"/UserViews/GroupingOptions?userId=" + adminID,
		"/Users/" + adminID + "/GroupingOptions",
	} {
		if _, status := getGroupingOptions(t, base, viewerToken, path); status != http.StatusForbidden {
			t.Errorf("%s as viewer: status %d, want 403", path, status)
		}
		if _, status := getGroupingOptions(t, base, adminToken, path); status != http.StatusOK {
			t.Errorf("%s as administrator: status %d, want 200", path, status)
		}
		if _, status := getGroupingOptions(t, base, "", path); status != http.StatusUnauthorized {
			t.Errorf("%s anonymous: status %d, want 401", path, status)
		}
	}
}

// TestGroupingOptionsEmptyServer is items.grouping.R1 on a server with no
// libraries: an empty JSON array, not null.
func TestGroupingOptionsEmptyServer(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "root", "rootpass")
	token := loginToken(t, ts.URL, "root", "rootpass")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/UserViews/GroupingOptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Errorf("body is %s, want []", raw)
	}
}
