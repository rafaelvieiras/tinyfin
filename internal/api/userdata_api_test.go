package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// udDo sends one request with the given token, JSON body (nil for none) and
// returns the status plus the decoded JSON object (nil if not an object).
func udDo(t *testing.T, base, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if raw, ok := body.([]byte); ok {
		rd = bytes.NewReader(raw)
	} else if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type udEnv struct {
	base                   string
	st                     storage.Store
	admin, alice, restrict storage.User
	adminTok, aliceTok     string
	restrictTok            string
	lib                    storage.LibraryID
	movies                 []storage.ItemID
	series, season, ep     storage.ItemID
}

func newUDEnv(t *testing.T) *udEnv {
	t.Helper()
	ts, st := newTestServer(t)
	e := &udEnv{base: ts.URL, st: st}
	e.admin = createTestAdminUser(t, st, "root", "rootpass")
	e.alice = createTestUser(t, st, "alice", "alicepass")
	e.restrict = createTestUser(t, st, "restricted", "restrictpass")
	e.restrict.Policy = `{"EnableAllFolders":false,"EnabledFolders":[]}`
	if err := st.UpdateUser(context.Background(), e.restrict); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	e.adminTok = loginToken(t, ts.URL, "root", "rootpass")
	e.aliceTok = loginToken(t, ts.URL, "alice", "alicepass")
	e.restrictTok = loginToken(t, ts.URL, "restricted", "restrictpass")

	e.lib = createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	rt := int64(60 * 10_000_000)
	for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
		it := storage.Item{Type: storage.ItemTypeMovie, LibraryID: e.lib, Name: n, Path: "/media/movies/" + n + ".mp4", RunTimeTicks: &rt}
		if err := st.UpsertItem(context.Background(), it); err != nil {
			t.Fatal(err)
		}
		e.movies = append(e.movies, mustFindItemByName(t, st, e.lib, n))
	}
	shows := createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)
	ser := storage.Item{Type: storage.ItemTypeSeries, LibraryID: shows, Name: "Show", Path: "/media/shows/Show"}
	if err := st.UpsertItem(context.Background(), ser); err != nil {
		t.Fatal(err)
	}
	e.series = mustFindItemByName(t, st, shows, "Show")
	sea := storage.Item{Type: storage.ItemTypeSeason, LibraryID: shows, Name: "Season 1", ParentID: &e.series, SeriesID: &e.series, Path: "/media/shows/Show/Season 01"}
	if err := st.UpsertItem(context.Background(), sea); err != nil {
		t.Fatal(err)
	}
	e.season = mustFindItemByName(t, st, shows, "Season 1")
	epi := storage.Item{Type: storage.ItemTypeEpisode, LibraryID: shows, Name: "Ep1", ParentID: &e.season, SeriesID: &e.series, Path: "/media/shows/Show/Season 01/e1.mp4"}
	if err := st.UpsertItem(context.Background(), epi); err != nil {
		t.Fatal(err)
	}
	e.ep = mustFindItemByName(t, st, shows, "Ep1")
	return e
}

func (e *udEnv) path(id storage.ItemID, q string) string {
	return "/UserItems/" + string(id) + "/UserData" + q
}

func TestUserDataReadDefaultsAndErrors(t *testing.T) {
	e := newUDEnv(t)
	item := e.movies[0]

	if code, _ := udDo(t, e.base, "", "GET", e.path(item, ""), nil); code != 401 {
		t.Errorf("anonymous GET = %d, want 401", code)
	}
	if code, _ := udDo(t, e.base, "", "POST", e.path(item, ""), map[string]any{}); code != 401 {
		t.Errorf("anonymous POST = %d, want 401", code)
	}

	code, got := udDo(t, e.base, e.aliceTok, "GET", e.path(item, ""), nil)
	if code != 200 || got["Played"] != false || got["IsFavorite"] != false || got["PlayCount"] != float64(0) || got["PlaybackPositionTicks"] != float64(0) {
		t.Errorf("defaults = %d %v", code, got)
	}
	if k, _ := got["Key"].(string); k == "" {
		t.Errorf("Key empty: %v", got)
	}

	for _, m := range []string{"GET", "POST"} {
		var body any
		if m == "POST" {
			body = map[string]any{"Played": true}
		}
		if c, _ := udDo(t, e.base, e.aliceTok, m, "/UserItems/11111111-2222-4333-8444-555555555555/UserData", body); c != 404 {
			t.Errorf("%s unknown item = %d, want 404", m, c)
		}
		if c, _ := udDo(t, e.base, e.aliceTok, m, "/UserItems/00000000-0000-0000-0000-000000000000/UserData", body); c != 400 {
			t.Errorf("%s zero id = %d, want 400", m, c)
		}
		if c, _ := udDo(t, e.base, e.restrictTok, m, e.path(item, ""), body); c != 404 {
			t.Errorf("%s hidden item = %d, want 404", m, c)
		}
		if c, _ := udDo(t, e.base, e.aliceTok, m, e.path(item, "?UserId="+string(e.admin.ID)), body); c != 403 {
			t.Errorf("%s other user as non-admin = %d, want 403", m, c)
		}
		if c, _ := udDo(t, e.base, e.adminTok, m, e.path(item, "?UserId=11111111-2222-4333-8444-555555555555"), body); c != 404 {
			t.Errorf("%s unknown user = %d, want 404", m, c)
		}
		if c, _ := udDo(t, e.base, e.aliceTok, m, e.path(item, "?UserId="+string(e.alice.ID)), body); c != 200 {
			t.Errorf("%s own id = %d, want 200", m, c)
		}
	}
	if c, _ := udDo(t, e.base, e.aliceTok, "POST", e.path(item, ""), []byte("{nope")); c != 400 {
		t.Errorf("malformed body = %d, want 400", c)
	}
}

func TestUserDataWriteSemantics(t *testing.T) {
	e := newUDEnv(t)
	item := e.movies[0]

	code, got := udDo(t, e.base, e.aliceTok, "POST", e.path(item, ""), map[string]any{"Played": true})
	if code != 200 || got["Played"] != true || got["PlayCount"] != float64(0) {
		t.Errorf("played = %d %v (PlayCount must stay 0)", code, got)
	}

	// Partial merge and empty object.
	udDo(t, e.base, e.aliceTok, "POST", e.path(item, ""), map[string]any{"IsFavorite": true})
	_, got = udDo(t, e.base, e.aliceTok, "POST", e.path(item, ""), map[string]any{})
	if got["IsFavorite"] != true || got["Played"] != true {
		t.Errorf("merge lost fields: %v", got)
	}

	// Read-only echo fields are ignored.
	_, got = udDo(t, e.base, e.aliceTok, "POST", e.path(item, ""), map[string]any{"Key": "k", "ItemId": "x", "PlayedPercentage": 50, "UnplayedItemCount": 9})
	if got["Key"] == "k" || got["ItemId"] == "x" || got["ItemId"] != string(item) {
		t.Errorf("echo fields not ignored: %v", got)
	}

	// Value fields, including nullable ones.
	_, got = udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[1], ""), map[string]any{
		"PlaybackPositionTicks": 12345, "PlayCount": 3, "LastPlayedDate": "2026-03-01T10:00:00Z", "Rating": 8, "Likes": true,
	})
	if got["PlaybackPositionTicks"] != float64(12345) || got["PlayCount"] != float64(3) || got["Rating"] != float64(8) || got["Likes"] != true {
		t.Errorf("fields = %v", got)
	}
	if s, _ := got["LastPlayedDate"].(string); len(s) < 19 || s[:19] != "2026-03-01T10:00:00" {
		t.Errorf("LastPlayedDate = %v", got["LastPlayedDate"])
	}
	// Null leaves the stored value untouched.
	_, got = udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[1], ""), []byte(`{"Rating":null,"PlayCount":null}`))
	if got["Rating"] != float64(8) || got["PlayCount"] != float64(3) {
		t.Errorf("null changed stored values: %v", got)
	}

	// Per-user isolation and admin writing for another user.
	_, got = udDo(t, e.base, e.adminTok, "GET", e.path(item, ""), nil)
	if got["Played"] != false || got["IsFavorite"] != false {
		t.Errorf("admin sees alice's data: %v", got)
	}
	code, _ = udDo(t, e.base, e.adminTok, "POST", e.path(e.movies[2], "?UserId="+string(e.alice.ID)), map[string]any{"IsFavorite": true})
	_, got = udDo(t, e.base, e.aliceTok, "GET", e.path(e.movies[2], ""), nil)
	if code != 200 || got["IsFavorite"] != true {
		t.Errorf("admin write for alice = %d %v", code, got)
	}
}

func TestUserDataFolderNoPropagationAndUnplayedCount(t *testing.T) {
	e := newUDEnv(t)
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.series, ""), map[string]any{"Played": true})
	_, got := udDo(t, e.base, e.aliceTok, "GET", e.path(e.ep, ""), nil)
	if got["Played"] != false {
		t.Errorf("folder write propagated to child: %v", got)
	}
	// The read of a folder carries the unplayed leaf count.
	_, got = udDo(t, e.base, e.aliceTok, "GET", e.path(e.series, ""), nil)
	if got["UnplayedItemCount"] != float64(1) {
		t.Errorf("series UnplayedItemCount = %v, want 1", got["UnplayedItemCount"])
	}
}

func resumeIDs(t *testing.T, e *udEnv, token, q string) (ids []string, total float64, raw map[string]any) {
	t.Helper()
	code, raw := udDo(t, e.base, token, "GET", "/UserItems/Resume"+q, nil)
	if code != 200 {
		t.Fatalf("GET /UserItems/Resume%s = %d", q, code)
	}
	items, _ := raw["Items"].([]any)
	for _, it := range items {
		ids = append(ids, it.(map[string]any)["Id"].(string))
	}
	total, _ = raw["TotalRecordCount"].(float64)
	return ids, total, raw
}

func TestResumeList(t *testing.T) {
	e := newUDEnv(t)
	bravo, charlie := string(e.movies[1]), string(e.movies[2])
	if c, _ := udDo(t, e.base, "", "GET", "/UserItems/Resume", nil); c != 401 {
		t.Errorf("anonymous = %d, want 401", c)
	}
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[1], ""), map[string]any{"PlaybackPositionTicks": 10000000, "LastPlayedDate": "2026-01-01T00:00:00Z"})
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[2], ""), map[string]any{"PlaybackPositionTicks": 20000000, "LastPlayedDate": "2026-02-01T00:00:00Z"})

	ids, total, _ := resumeIDs(t, e, e.aliceTok, "")
	if total != 2 || len(ids) != 2 || ids[0] != charlie || ids[1] != bravo {
		t.Errorf("order = %v total %v, want [charlie bravo]", ids, total)
	}

	// Paging and EnableTotalRecordCount.
	ids, total, raw := resumeIDs(t, e, e.aliceTok, "?StartIndex=1&Limit=1")
	if len(ids) != 1 || ids[0] != bravo || total != 2 || raw["StartIndex"] != float64(1) {
		t.Errorf("paging = %v %v %v", ids, total, raw["StartIndex"])
	}
	if _, total, _ = resumeIDs(t, e, e.aliceTok, "?Limit=1&EnableTotalRecordCount=false"); total != 1 {
		t.Errorf("EnableTotalRecordCount=false total = %v, want 1", total)
	}

	// Filters.
	cases := []struct {
		q    string
		want float64
	}{
		{"?MediaTypes=Video", 2}, {"?MediaTypes=Audio", 0},
		{"?IncludeItemTypes=Movie", 2}, {"?IncludeItemTypes=Episode", 0},
		{"?ExcludeItemTypes=Movie", 0}, {"?IncludeItemTypes=Movie&ExcludeItemTypes=Movie", 2},
		{"?ParentId=" + string(e.lib), 2}, {"?ParentId=11111111-2222-4333-8444-555555555555", 0},
		{"?SearchTerm=Bravo", 1}, {"?ExcludeActiveSessions=true", 2},
	}
	for _, c := range cases {
		if _, total, _ := resumeIDs(t, e, e.aliceTok, c.q); total != c.want {
			t.Errorf("%s total = %v, want %v", c.q, total, c.want)
		}
	}

	// Per user, and administrators acting for another user.
	if _, total, _ = resumeIDs(t, e, e.adminTok, ""); total != 0 {
		t.Errorf("admin own total = %v, want 0", total)
	}
	if _, total, _ = resumeIDs(t, e, e.restrictTok, ""); total != 0 {
		t.Errorf("restricted total = %v, want 0", total)
	}
	if ids, total, _ = resumeIDs(t, e, e.adminTok, "?UserId="+string(e.alice.ID)); total != 2 || ids[0] != charlie {
		t.Errorf("admin for alice = %v %v", ids, total)
	}

	// UserId rules.
	if c, _ := udDo(t, e.base, e.aliceTok, "GET", "/UserItems/Resume?UserId="+string(e.admin.ID), nil); c != 403 {
		t.Errorf("other user as non-admin = %d, want 403", c)
	}
	if c, _ := udDo(t, e.base, e.adminTok, "GET", "/UserItems/Resume?UserId=11111111-2222-4333-8444-555555555555", nil); c != 404 {
		t.Errorf("unknown user = %d, want 404", c)
	}
	if c, _ := udDo(t, e.base, e.adminTok, "GET", "/UserItems/Resume?UserId=nope", nil); c != 400 {
		t.Errorf("malformed user = %d, want 400", c)
	}

	// EnableUserData.
	_, _, raw = resumeIDs(t, e, e.aliceTok, "?EnableUserData=false")
	if _, has := raw["Items"].([]any)[0].(map[string]any)["UserData"]; has {
		t.Error("EnableUserData=false still carries UserData")
	}
	_, _, raw = resumeIDs(t, e, e.aliceTok, "")
	if _, has := raw["Items"].([]any)[0].(map[string]any)["UserData"]; !has {
		t.Error("UserData missing by default")
	}

	// Leaves the list once position is zeroed or played.
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[2], ""), map[string]any{"PlaybackPositionTicks": 0})
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[1], ""), map[string]any{"Played": true})
	if _, total, _ = resumeIDs(t, e, e.aliceTok, ""); total != 0 {
		t.Errorf("after reset/played total = %v, want 0", total)
	}
}

func TestProgressBelowMinimumResetsSavedPosition(t *testing.T) {
	e := newUDEnv(t)
	udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[0], ""), map[string]any{"PlaybackPositionTicks": 20000000})
	if _, total, _ := resumeIDs(t, e, e.aliceTok, ""); total != 1 {
		t.Fatalf("seeded total = %v, want 1", total)
	}
	// 100000 ticks of a 60 s runtime is far below the 5% minimum.
	code, _ := udDo(t, e.base, e.aliceTok, "POST", "/Sessions/Playing/Progress", map[string]any{"ItemId": string(e.movies[0]), "PositionTicks": 100000})
	if code != 204 {
		t.Fatalf("progress = %d, want 204", code)
	}
	if _, total, _ := resumeIDs(t, e, e.aliceTok, ""); total != 0 {
		t.Errorf("total after below-minimum progress = %v, want 0", total)
	}
}
