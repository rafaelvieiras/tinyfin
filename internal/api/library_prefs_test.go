package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file covers the per-account preferences the preferences page saves
// (POST /Users/{userId}/Configuration) and what they change outside it:
// items.views.R6 (OrderedViews), items.views.R7 (MyMediaExcludes and
// includeHidden) and items.latest.R5/R6 (HidePlayedInLatest and
// LatestItemsExcludes). The conformance scenario users.preferences.save
// asserts the same behavior over the wire.

func dto(id, name string) baseItemDto {
	return baseItemDto{ID: id, Name: name}
}

// TestOrderViews is items.views.R6: the saved order comes first, in its own
// order; a view the list does not name keeps the default order behind them;
// an identifier that names nothing and a repeat are both ignored.
func TestOrderViews(t *testing.T) {
	movies := "11111111111111111111111111111111"
	shows := "22222222222222222222222222222222"
	music := "33333333333333333333333333333333"

	names := func(items []baseItemDto) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Name)
		}
		return out
	}
	equal := func(t *testing.T, got []baseItemDto, want ...string) {
		t.Helper()
		if fmt.Sprint(names(got)) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", names(got), want)
		}
	}

	items := []baseItemDto{dto(movies, "Movies"), dto(music, "Music"), dto(shows, "Shows")}

	equal(t, orderViews(items, nil), "Movies", "Music", "Shows")
	equal(t, orderViews(items, []string{shows, movies}), "Shows", "Movies", "Music")
	equal(t, orderViews(items, []string{shows}), "Shows", "Movies", "Music")
	// An identifier that names nobody is ignored, and so is a repeat.
	equal(t, orderViews(items, []string{"0123456789abcdef0123456789abcdef", shows, shows}), "Shows", "Movies", "Music")
	// The dashed spelling of the same identifier matches.
	equal(t, orderViews(items, []string{"22222222-2222-2222-2222-222222222222"}), "Shows", "Movies", "Music")
	if got := orderViews(items, []string{"not-a-guid"}); len(got) != 3 {
		t.Fatalf("an unusable list changed the order: %v", names(got))
	}
}

// TestDropExcludedViews is items.views.R7: the libraries named by
// MyMediaExcludes leave the list, and nothing else does.
func TestDropExcludedViews(t *testing.T) {
	movies := "11111111111111111111111111111111"
	shows := "22222222222222222222222222222222"
	items := []baseItemDto{dto(movies, "Movies"), dto(shows, "Shows")}

	if got := dropExcludedViews(items, nil); len(got) != 2 {
		t.Fatalf("an empty exclude list dropped something: %d items", len(got))
	}
	got := dropExcludedViews(items, []string{"11111111-1111-1111-1111-111111111111"})
	if len(got) != 1 || got[0].Name != "Shows" {
		t.Fatalf("dropExcludedViews = %v, want [Shows]", got)
	}
	if got := dropExcludedViews(items, []string{"0123456789abcdef0123456789abcdef"}); len(got) != 2 {
		t.Fatalf("an identifier that names no view dropped something: %d items", len(got))
	}
}

// TestDropExcludedLibraryItems is items.latest.R6: items of an excluded
// library leave the unscoped latest row.
func TestDropExcludedLibraryItems(t *testing.T) {
	movies := storage.LibraryID("11111111111111111111111111111111")
	shows := storage.LibraryID("22222222222222222222222222222222")
	items := []storage.Item{
		{ID: "a1", Name: "Alpha", LibraryID: movies},
		{ID: "b1", Name: "Show", LibraryID: shows},
	}
	if got := dropExcludedLibraryItems(items, nil); len(got) != 2 {
		t.Fatalf("an empty exclude list dropped something: %d items", len(got))
	}
	got := dropExcludedLibraryItems(items, []string{string(movies)})
	if len(got) != 1 || got[0].Name != "Show" {
		t.Fatalf("dropExcludedLibraryItems = %v, want [Show]", got)
	}
}

// newPrefsEnv serves a server with a Movies and a Shows library, each holding
// one item, and returns the base URL, an admin token and their account.
func newPrefsEnv(t *testing.T) (base, token, adminID string, movies, shows storage.LibraryID, alpha storage.ItemID) {
	t.Helper()
	ts, st := newTestServer(t)
	ctx := context.Background()
	root := t.TempDir()
	movies = storage.LibraryID("11111111111111111111111111111111")
	shows = storage.LibraryID("22222222222222222222222222222222")
	for _, lib := range []storage.Library{
		{ID: movies, Name: "Movies", CollectionType: storage.CollectionTypeMovies, Paths: []string{root}},
		{ID: shows, Name: "Shows", CollectionType: storage.CollectionTypeTVShows, Paths: []string{root}},
	} {
		if err := st.UpsertLibrary(ctx, lib); err != nil {
			t.Fatalf("UpsertLibrary(%q): %v", lib.Name, err)
		}
	}
	now := time.Now().UTC()
	alpha = storage.ItemID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1")
	for _, it := range []storage.Item{
		{ID: alpha, Type: storage.ItemTypeMovie, Name: "Alpha Movie", SortName: "Alpha Movie", LibraryID: movies, DateCreated: now.Add(-time.Hour)},
		{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", Type: storage.ItemTypeSeries, Name: "Test Show", SortName: "Test Show", LibraryID: shows, DateCreated: now.Add(-2 * time.Hour)},
	} {
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatalf("UpsertItem(%q): %v", it.Name, err)
		}
	}
	admin := createTestAdminUser(t, st, "prefsroot", "prefspass")
	return ts.URL, loginToken(t, ts.URL, "prefsroot", "prefspass"), string(admin.ID), movies, shows, alpha
}

func doPrefsJSON(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// viewNames lists the names a views route answers with, and its status.
func viewNames(t *testing.T, url, token string) ([]string, int) {
	t.Helper()
	resp := doPrefsJSON(t, http.MethodGet, url, token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var page struct {
		Items []struct{ Name string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	names := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		names = append(names, it.Name)
	}
	return names, resp.StatusCode
}

// latestNames lists the names GET /Items/Latest answers with.
func latestNames(t *testing.T, base, path, token string) []string {
	t.Helper()
	resp := doPrefsJSON(t, http.MethodGet, base+path, token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var items []struct{ Name string }
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func saveConfig(t *testing.T, base, token, adminID string, body any) {
	t.Helper()
	resp := doPrefsJSON(t, http.MethodPost, base+"/Users/"+adminID+"/Configuration", token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST Configuration: status %d, want 204", resp.StatusCode)
	}
}

// TestUserViewsFollowSavedPreferences is items.views.R6/R7 end to end: what
// the preferences page saves is what GET /UserViews and GET /Users/{id}/Views
// serve, and the grouping options list is left alone.
func TestUserViewsFollowSavedPreferences(t *testing.T) {
	base, token, adminID, movies, shows, _ := newPrefsEnv(t)

	if names, status := viewNames(t, base+"/UserViews", token); status != http.StatusOK || fmt.Sprint(names) != "[Movies Shows]" {
		t.Fatalf("default /UserViews = %v (status %d), want [Movies Shows]", names, status)
	}

	// The page saves a new order.
	saveConfig(t, base, token, adminID, map[string]any{
		"OrderedViews": []string{string(shows), string(movies)},
	})
	for _, path := range []string{"/UserViews", "/Users/" + adminID + "/Views"} {
		names, status := viewNames(t, base+path, token)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, status)
		}
		if fmt.Sprint(names) != "[Shows Movies]" {
			t.Fatalf("GET %s = %v, want [Shows Movies]", path, names)
		}
	}

	// The grouping options list has its own order and ignores OrderedViews.
	opts, status := getGroupingOptions(t, base, token, "/UserViews/GroupingOptions")
	if status != http.StatusOK {
		t.Fatalf("GroupingOptions: status %d", status)
	}
	if len(opts) != 2 || opts[0].Name != "Movies" || opts[1].Name != "Shows" {
		t.Fatalf("GroupingOptions = %v, want [Movies Shows]", opts)
	}

	// The page unchecks a library from My Media.
	saveConfig(t, base, token, adminID, map[string]any{
		"OrderedViews":    []string{string(shows), string(movies)},
		"MyMediaExcludes": []string{string(movies)},
	})
	if names, _ := viewNames(t, base+"/UserViews", token); containsName(names, "Movies") {
		t.Fatalf("an excluded library is still listed: %v", names)
	}
	if names, _ := viewNames(t, base+"/UserViews?includeHidden=true", token); fmt.Sprint(names) != "[Shows Movies]" {
		t.Fatalf("includeHidden = %v, want [Shows Movies]", names)
	}
	if names, _ := viewNames(t, base+"/Users/"+adminID+"/Views?includeHidden=true", token); fmt.Sprint(names) != "[Shows Movies]" {
		t.Fatalf("legacy includeHidden = %v, want [Shows Movies]", names)
	}
	if _, status := viewNames(t, base+"/UserViews?includeHidden=1", token); status != http.StatusBadRequest {
		t.Fatalf("includeHidden=1: status %d, want 400", status)
	}
}

// TestUserLatestFollowsSavedPreferences is items.latest.R5/R6: the played
// filter and the excluded libraries the preferences page saves change the
// "recently added" row, and an explicit isPlayed beats the preference.
func TestUserLatestFollowsSavedPreferences(t *testing.T) {
	base, token, adminID, movies, _, alpha := newPrefsEnv(t)

	if names := latestNames(t, base, "/Items/Latest?limit=10", token); !containsName(names, "Alpha Movie") {
		t.Fatalf("Alpha Movie missing from the default row: %v", names)
	}

	// Watched media leaves the row (HidePlayedInLatest defaults to true).
	resp := doPrefsJSON(t, http.MethodPost, base+"/Users/"+adminID+"/PlayedItems/"+string(alpha), token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mark played: status %d", resp.StatusCode)
	}
	if names := latestNames(t, base, "/Items/Latest?limit=10", token); containsName(names, "Alpha Movie") {
		t.Fatalf("a played movie is still in the row: %v", names)
	}
	// An isPlayed the request asks for wins over the preference.
	if names := latestNames(t, base, "/Items/Latest?limit=10&isPlayed=true", token); !containsName(names, "Alpha Movie") {
		t.Fatalf("isPlayed=true did not bring the played movie back: %v", names)
	}

	saveConfig(t, base, token, adminID, map[string]any{"HidePlayedInLatest": false})
	if names := latestNames(t, base, "/Items/Latest?limit=10", token); !containsName(names, "Alpha Movie") {
		t.Fatalf("HidePlayedInLatest=false did not restore the played movie: %v", names)
	}

	// An excluded library leaves the row. HidePlayedInLatest stays off so
	// the exclusion is what the assertion measures, not the played filter.
	saveConfig(t, base, token, adminID, map[string]any{
		"HidePlayedInLatest":  false,
		"LatestItemsExcludes": []string{string(movies)},
	})
	if names := latestNames(t, base, "/Items/Latest?limit=10", token); containsName(names, "Alpha Movie") {
		t.Fatalf("an excluded library is still in the row: %v", names)
	}
	// Scoped to that library by name, the caller asked for it.
	if names := latestNames(t, base, "/Items/Latest?limit=10&parentId="+string(movies), token); !containsName(names, "Alpha Movie") {
		t.Fatalf("a scoped request lost the library's own media: %v", names)
	}
}

// TestUserItemsLatestRoute is items.latest.R5's routing: the per-user spelling
// exists, and it answers the same authorization the legacy views route does.
func TestUserItemsLatestRoute(t *testing.T) {
	base, token, adminID, _, _, _ := newPrefsEnv(t)

	if names := latestNames(t, base, "/Users/"+adminID+"/Items/Latest?limit=10", token); !containsName(names, "Alpha Movie") {
		t.Fatalf("the per-user route answered %v", names)
	}

	for _, tc := range []struct {
		name   string
		path   string
		token  string
		status int
	}{
		{"anonymous", "/Users/" + adminID + "/Items/Latest", "", http.StatusUnauthorized},
		{"an identifier that names nobody", "/Users/0123456789abcdef0123456789abcdef/Items/Latest", token, http.StatusNotFound},
		{"a malformed identifier", "/Users/nope/Items/Latest", token, http.StatusBadRequest},
	} {
		resp := doPrefsJSON(t, http.MethodGet, base+tc.path, tc.token, nil)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
}
