package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// psEnv is a small fixture shared by the playstate tests: one admin, one
// regular user, a movie library with a 6 s movie, and a three-episode show.
type psEnv struct {
	t        *testing.T
	ts       *httptest.Server
	st       *sqlite.Store
	admin    storage.User
	alice    storage.User
	adminTok string
	aliceTok string
	movie    storage.ItemID
	show     storage.ItemID
	season1  storage.ItemID
	season2  storage.ItemID
	eps      []storage.ItemID
	libID    storage.LibraryID
}

const psRuntime = int64(60_000_000)

func newPSEnv(t *testing.T) *psEnv {
	t.Helper()
	ts, st := newTestServer(t)
	e := &psEnv{t: t, ts: ts, st: st}
	e.admin = createTestAdminUser(t, st, "root", "rootpass")
	e.alice = createTestUser(t, st, "alice", "alicepass")
	e.adminTok = loginToken(t, ts.URL, "root", "rootpass")
	e.aliceTok = loginToken(t, ts.URL, "alice", "alicepass")
	e.libID = createTestLibrary(t, st, "Mixed", storage.CollectionTypeMovies)
	ctx := context.Background()
	rt := psRuntime
	if err := st.UpsertItem(ctx, storage.Item{Type: storage.ItemTypeMovie, LibraryID: e.libID, Name: "Movie", Path: "/media/m.mkv", RunTimeTicks: &rt}); err != nil {
		t.Fatal(err)
	}
	e.movie = mustFindItemByName(t, st, e.libID, "Movie")
	if err := st.UpsertItem(ctx, storage.Item{Type: storage.ItemTypeSeries, LibraryID: e.libID, Name: "Show", Path: "/media/show"}); err != nil {
		t.Fatal(err)
	}
	e.show = mustFindItemByName(t, st, e.libID, "Show")
	for i, name := range []string{"S1", "S2"} {
		n := i + 1
		if err := st.UpsertItem(ctx, storage.Item{Type: storage.ItemTypeSeason, LibraryID: e.libID, ParentID: &e.show, SeriesID: &e.show, Name: name, Path: "/media/show/" + name, IndexNumber: &n}); err != nil {
			t.Fatal(err)
		}
	}
	e.season1 = mustFindItemByName(t, st, e.libID, "S1")
	e.season2 = mustFindItemByName(t, st, e.libID, "S2")
	for _, ep := range []struct {
		name   string
		season storage.ItemID
	}{{"E1", e.season1}, {"E2", e.season1}, {"E3", e.season2}} {
		season := ep.season
		if err := st.UpsertItem(ctx, storage.Item{Type: storage.ItemTypeEpisode, LibraryID: e.libID, ParentID: &season, SeriesID: &e.show, SeasonID: &season, Name: ep.name, Path: "/media/show/" + ep.name + ".mkv", RunTimeTicks: &rt}); err != nil {
			t.Fatal(err)
		}
		e.eps = append(e.eps, mustFindItemByName(t, st, e.libID, ep.name))
	}
	return e
}

// do sends a request with a Token-only authorization header (or anonymous
// when token is empty) and returns the status and body.
func (e *psEnv) do(token, method, path, body string) (int, []byte) {
	return e.doDevice(token, "", method, path, body)
}

func (e *psEnv) doDevice(token, device, method, path, body string) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		h := `MediaBrowser Token="` + token + `"`
		if device != "" {
			h += `, Client="TestClient", Device="TestDevice", DeviceId="` + device + `", Version="1.0"`
		}
		req.Header.Set("Authorization", h)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *psEnv) userData(token string, id storage.ItemID) map[string]any {
	e.t.Helper()
	code, b := e.do(token, http.MethodGet, "/Items/"+string(id), "")
	if code != http.StatusOK {
		e.t.Fatalf("GET /Items/%s = %d", id, code)
	}
	var m struct{ UserData map[string]any }
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m.UserData
}

func jsonMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("bad JSON %q: %v", b, err)
	}
	return m
}

func wantStatus(t *testing.T, got, want int, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status = %d, want %d", what, got, want)
	}
}

func num(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func TestMarkPlayedDateRules(t *testing.T) {
	e := newPSEnv(t)
	p := "/UserPlayedItems/" + string(e.movie)

	code, b := e.do(e.aliceTok, http.MethodPost, p, "")
	wantStatus(t, code, 200, "mark no date")
	m := jsonMap(t, b)
	if m["Played"] != true || num(m, "PlayCount") != 1 || num(m, "PlaybackPositionTicks") != 0 {
		t.Fatalf("mark no date = %v", m)
	}
	first, ok := m["LastPlayedDate"].(string)
	if !ok || first == "" {
		t.Fatalf("LastPlayedDate missing: %v", m)
	}
	// idempotent without a date
	_, b = e.do(e.aliceTok, http.MethodPost, p, "")
	m = jsonMap(t, b)
	if num(m, "PlayCount") != 1 || m["LastPlayedDate"] != first {
		t.Fatalf("second mark changed state: %v", m)
	}
	// dated mark increments and normalizes to UTC; query name is case-insensitive
	_, b = e.do(e.aliceTok, http.MethodPost, p+"?DatePlayed=2026-01-02T05:04:05%2B02:00", "")
	m = jsonMap(t, b)
	if num(m, "PlayCount") != 2 || !strings.HasPrefix(m["LastPlayedDate"].(string), "2026-01-02T03:04:05") {
		t.Fatalf("dated mark = %v", m)
	}
	code, _ = e.do(e.aliceTok, http.MethodPost, p+"?datePlayed=yesterday", "")
	wantStatus(t, code, 400, "invalid date")
	if got := e.userData(e.aliceTok, e.movie); num(got, "PlayCount") != 2 {
		t.Fatalf("invalid date changed state: %v", got)
	}
}

func TestMarkUnplayedResetsAndKeepsFavorite(t *testing.T) {
	e := newPSEnv(t)
	id := string(e.movie)
	code, b := e.do(e.aliceTok, http.MethodPost, "/UserFavoriteItems/"+id, "")
	wantStatus(t, code, 200, "favorite")
	if jsonMap(t, b)["IsFavorite"] != true {
		t.Fatalf("favorite response = %s", b)
	}
	if err := e.st.SetPlaystate(context.Background(), e.alice.ID, e.movie, storage.Playstate{IsFavorite: true, PlaybackPositionTicks: 20_000_000, PlayCount: 3, Played: true}); err != nil {
		t.Fatal(err)
	}
	code, b = e.do(e.aliceTok, http.MethodDelete, "/UserPlayedItems/"+id, "")
	wantStatus(t, code, 200, "unmark")
	m := jsonMap(t, b)
	if m["Played"] != false || num(m, "PlayCount") != 0 || num(m, "PlaybackPositionTicks") != 0 || m["IsFavorite"] != true {
		t.Fatalf("unmark = %v", m)
	}
	if _, has := m["LastPlayedDate"]; has {
		t.Fatalf("LastPlayedDate present after unmark: %v", m)
	}
}

func TestMarkPlayedTargetRules(t *testing.T) {
	e := newPSEnv(t)
	id := string(e.movie)
	p := "/UserPlayedItems/" + id
	cases := []struct {
		name, token, method, path string
		want                      int
	}{
		{"anonymous", "", http.MethodPost, p, 401},
		{"unknown item", e.aliceTok, http.MethodPost, "/UserPlayedItems/7f3c1d2e-9a4b-4c5d-8e6f-0a1b2c3d4e5f", 404},
		{"malformed item", e.aliceTok, http.MethodPost, "/UserPlayedItems/not-a-guid", 400},
		{"zero item", e.aliceTok, http.MethodDelete, "/UserPlayedItems/00000000-0000-0000-0000-000000000000", 400},
		{"non-admin other user", e.aliceTok, http.MethodPost, p + "?userId=" + string(e.admin.ID), 403},
		{"non-admin unknown user", e.aliceTok, http.MethodPost, p + "?userId=7f3c1d2e-9a4b-4c5d-8e6f-0a1b2c3d4e5f", 403},
		{"admin unknown user", e.adminTok, http.MethodPost, p + "?userId=7f3c1d2e-9a4b-4c5d-8e6f-0a1b2c3d4e5f", 404},
		{"malformed user", e.adminTok, http.MethodDelete, p + "?userId=zzz", 400},
		{"own id", e.aliceTok, http.MethodPost, p + "?userId=" + string(e.alice.ID), 200},
	}
	for _, c := range cases {
		code, b := e.do(c.token, c.method, c.path, "")
		if code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, code, c.want, b)
		}
		if c.name == "unknown item" {
			if jsonMap(t, b)["status"] != float64(404) {
				t.Errorf("%s: not a problem document: %s", c.name, b)
			}
		}
	}
	// admin on behalf changes the target user only
	code, _ := e.do(e.adminTok, http.MethodPost, p+"?userId="+string(e.alice.ID), "")
	wantStatus(t, code, 200, "admin on behalf")
	if got := e.userData(e.adminTok, e.movie); got["Played"] == true {
		t.Fatalf("admin's own data changed: %v", got)
	}
	// legacy alias: path user
	code, b := e.do(e.aliceTok, http.MethodPost, "/Users/"+string(e.admin.ID)+"/PlayedItems/"+id, "")
	wantStatus(t, code, 403, "legacy other user")
	code, b = e.do(e.aliceTok, http.MethodPost, "/Users/"+string(e.alice.ID)+"/PlayedItems/"+id+"?DatePlayed=2026-03-04T05:06:07Z", "")
	wantStatus(t, code, 200, "legacy mark")
	if !strings.HasPrefix(jsonMap(t, b)["LastPlayedDate"].(string), "2026-03-04T05:06:07") {
		t.Fatalf("legacy date not honored: %s", b)
	}
}

func TestMarkPlayedHiddenItem(t *testing.T) {
	e := newPSEnv(t)
	// a user restricted to no folders
	u := createTestUser(t, e.st, "locked", "lockedpass")
	u.Policy = `{"EnableAllFolders":false,"EnabledFolders":[]}`
	if err := e.st.UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	tok := loginToken(t, e.ts.URL, "locked", "lockedpass")
	code, _ := e.do(tok, http.MethodPost, "/UserPlayedItems/"+string(e.movie), "")
	wantStatus(t, code, 404, "hidden mark")
	// A report is accepted and, as on the reference, writes the user data of
	// the hidden item (playstate.start.R6).
	code, _ = e.do(tok, http.MethodPost, "/Sessions/Playing", `{"ItemId":"`+string(e.movie)+`"}`)
	wantStatus(t, code, 204, "hidden start")
	st, _ := e.st.GetPlaystate(context.Background(), u.ID, e.movie)
	if st.PlayCount != 1 {
		t.Fatalf("hidden item data not written: %+v", st)
	}
	// ...but the hidden item is never exposed as now playing.
	code, b := e.do(e.adminTok, http.MethodGet, "/Sessions", "")
	wantStatus(t, code, 200, "sessions")
	if strings.Contains(string(b), string(e.movie)) {
		t.Fatalf("hidden item shown as now playing: %s", b)
	}
}

func TestFolderMarkIsDerived(t *testing.T) {
	e := newPSEnv(t)
	// seed one leaf position, then mark the series played
	if err := e.st.SetPlaystate(context.Background(), e.alice.ID, e.eps[0], storage.Playstate{PlaybackPositionTicks: 20_000_000}); err != nil {
		t.Fatal(err)
	}
	code, b := e.do(e.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e.show)+"?datePlayed=2026-01-02T03:04:05Z", "")
	wantStatus(t, code, 200, "series played")
	m := jsonMap(t, b)
	if m["Played"] != true || num(m, "UnplayedItemCount") != 0 || num(m, "PlayCount") != 0 || num(m, "PlayedPercentage") != 100 {
		t.Fatalf("folder mark = %v", m)
	}
	if _, has := m["LastPlayedDate"]; has {
		t.Fatalf("folder LastPlayedDate set: %v", m)
	}
	for i, ep := range e.eps {
		got := e.userData(e.aliceTok, ep)
		if got["Played"] != false || num(got, "PlayCount") != 0 {
			t.Fatalf("leaf %d changed: %v", i, got)
		}
	}
	if got := e.userData(e.aliceTok, e.eps[0]); num(got, "PlaybackPositionTicks") != 20_000_000 {
		t.Fatalf("leaf position lost: %v", got)
	}
	if got := e.userData(e.aliceTok, e.season2); got["Played"] != true || num(got, "UnplayedItemCount") != 0 {
		t.Fatalf("season below marked series = %v", got)
	}
	// unmarking one leaf flips the ancestors
	code, _ = e.do(e.aliceTok, http.MethodDelete, "/UserPlayedItems/"+string(e.eps[0]), "")
	wantStatus(t, code, 200, "unmark leaf")
	if got := e.userData(e.aliceTok, e.show); got["Played"] != false || num(got, "UnplayedItemCount") != 1 {
		t.Fatalf("series after leaf unmark = %v", got)
	}
	// a leaf that never had a row is unmarked the same way
	e.do(e.aliceTok, http.MethodDelete, "/UserPlayedItems/"+string(e.eps[2]), "")
	if got := e.userData(e.aliceTok, e.show); num(got, "UnplayedItemCount") != 2 {
		t.Fatalf("series after unmarking an untouched leaf = %v", got)
	}
	// folder unmark counts every leaf, even individually played ones
	e.do(e.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e.eps[1]), "")
	code, b = e.do(e.aliceTok, http.MethodDelete, "/UserPlayedItems/"+string(e.show), "")
	wantStatus(t, code, 200, "series unplayed")
	m = jsonMap(t, b)
	if m["Played"] != false || num(m, "UnplayedItemCount") != 3 || num(m, "PlayedPercentage") != 0 {
		t.Fatalf("folder unmark = %v", m)
	}
	if got := e.userData(e.aliceTok, e.eps[1]); got["Played"] != true || num(got, "PlayCount") != 1 {
		t.Fatalf("leaf data changed by folder unmark: %v", got)
	}
}

func TestLeavesUpdateAncestors(t *testing.T) {
	e := newPSEnv(t)
	e.do(e.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e.eps[0]), "")
	if got := e.userData(e.aliceTok, e.show); got["Played"] != false || num(got, "UnplayedItemCount") != 2 {
		t.Fatalf("series after one leaf = %v", got)
	}
	e.do(e.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e.eps[1]), "")
	e.do(e.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e.eps[2]), "")
	if got := e.userData(e.aliceTok, e.show); got["Played"] != true || num(got, "UnplayedItemCount") != 0 {
		t.Fatalf("series after all leaves = %v", got)
	}
	// marking only one season leaves the series partially played
	e2 := newPSEnv(t)
	e2.do(e2.aliceTok, http.MethodPost, "/UserPlayedItems/"+string(e2.season1), "")
	if got := e2.userData(e2.aliceTok, e2.show); got["Played"] != false || num(got, "UnplayedItemCount") != 1 {
		t.Fatalf("series after season mark = %v", got)
	}
}

func (e *psEnv) report(token, kind, body string) int {
	code, _ := e.doDevice(token, "dev-"+token[:6], http.MethodPost, "/Sessions/Playing"+kind, body)
	return code
}

func TestPlaybackReportingThresholds(t *testing.T) {
	type step struct{ kind, body string }
	cases := []struct {
		name        string
		steps       []step
		played      bool
		count       int
		pos         float64
		wantLastSet bool
	}{
		{"start counts", []step{{"", `{"ItemId":"%s"}`}}, false, 1, 0, true},
		{"progress below minimum", []step{{"", `{"ItemId":"%s"}`}, {"/Progress", `{"ItemId":"%s","PositionTicks":100000}`}}, false, 1, 0, true},
		{"progress mid short video", []step{{"/Progress", `{"ItemId":"%s","PositionTicks":25000000}`}}, true, 0, 0, true},
		{"progress above maximum", []step{{"/Progress", `{"ItemId":"%s","PositionTicks":59500000}`}}, true, 0, 0, true},
		{"progress without position", []step{{"", `{"ItemId":"%s"}`}, {"/Progress", `{"ItemId":"%s"}`}}, false, 1, 0, true},
		{"stopped without position", []step{{"", `{"ItemId":"%s"}`}, {"/Stopped", `{"ItemId":"%s"}`}}, true, 2, 0, true},
		{"stopped without start", []step{{"/Stopped", `{"ItemId":"%s"}`}}, true, 1, 0, true},
		{"stopped with position keeps count", []step{{"", `{"ItemId":"%s"}`}, {"/Stopped", `{"ItemId":"%s","PositionTicks":25000000}`}}, true, 1, 0, true},
		{"stopped beyond runtime", []step{{"/Stopped", `{"ItemId":"%s","PositionTicks":6000000000}`}}, true, 0, 0, true},
		{"stopped below minimum", []step{{"", `{"ItemId":"%s"}`}, {"/Stopped", `{"ItemId":"%s","PositionTicks":100000}`}}, false, 1, 0, true},
		{"stopped failed writes nothing", []step{{"", `{"ItemId":"%s"}`}, {"/Stopped", `{"ItemId":"%s","PositionTicks":59500000,"Failed":true}`}}, false, 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newPSEnv(t)
			for _, s := range c.steps {
				if code := e.report(e.aliceTok, s.kind, strings.ReplaceAll(s.body, "%s", string(e.movie))); code != 204 {
					t.Fatalf("%s %s = %d", s.kind, s.body, code)
				}
			}
			got := e.userData(e.aliceTok, e.movie)
			if got["Played"] != c.played || num(got, "PlayCount") != float64(c.count) || num(got, "PlaybackPositionTicks") != c.pos {
				t.Fatalf("user data = %v", got)
			}
		})
	}
}

func TestPlaybackReportingLongRuntimeSavesPosition(t *testing.T) {
	e := newPSEnv(t)
	long := int64(6_000_000_000) // 600 s
	if err := e.st.UpsertItem(context.Background(), storage.Item{Type: storage.ItemTypeMovie, LibraryID: e.libID, Name: "Long", Path: "/media/l.mkv", RunTimeTicks: &long}); err != nil {
		t.Fatal(err)
	}
	id := mustFindItemByName(t, e.st, e.libID, "Long")
	body := func(pos int64) string {
		b, _ := json.Marshal(map[string]any{"ItemId": id, "PositionTicks": pos})
		return string(b)
	}
	if code := e.report(e.aliceTok, "/Progress", body(2_000_000_000)); code != 204 {
		t.Fatal(code)
	}
	got := e.userData(e.aliceTok, id)
	if got["Played"] != false || num(got, "PlaybackPositionTicks") != 2_000_000_000 {
		t.Fatalf("progress mid = %v", got)
	}
	// it enters the resume list, and ExcludeActiveSessions hides it while playing
	_, b := e.do(e.aliceTok, http.MethodGet, "/UserItems/Resume", "")
	if n := num(jsonMap(t, b), "TotalRecordCount"); n != 1 {
		t.Fatalf("resume = %s", b)
	}
	e.doDevice(e.aliceTok, "dev-"+e.aliceTok[:6], http.MethodPost, "/Sessions/Playing", body(2_000_000_000))
	_, b = e.do(e.aliceTok, http.MethodGet, "/UserItems/Resume?ExcludeActiveSessions=true", "")
	if n := num(jsonMap(t, b), "TotalRecordCount"); n != 0 {
		t.Fatalf("resume with exclusion = %s", b)
	}
	// stopping above the maximum finishes it
	if code := e.report(e.aliceTok, "/Stopped", body(5_800_000_000)); code != 204 {
		t.Fatal(code)
	}
	got = e.userData(e.aliceTok, id)
	if got["Played"] != true || num(got, "PlaybackPositionTicks") != 0 {
		t.Fatalf("stopped above max = %v", got)
	}
	_, b = e.do(e.aliceTok, http.MethodGet, "/UserItems/Resume?ExcludeActiveSessions=true", "")
	if n := num(jsonMap(t, b), "TotalRecordCount"); n != 0 {
		t.Fatalf("resume after finish = %s", b)
	}
}

func TestPlaybackReportValidation(t *testing.T) {
	e := newPSEnv(t)
	id := string(e.movie)
	bad := []struct{ kind, body string }{
		{"", `{"ItemId":"not-a-guid"}`},
		{"", `{"ItemId":`},
		{"", ``},
		{"", `{"ItemId":"` + id + `","PlayMethod":"Teleport"}`},
		{"", `{"ItemId":"` + id + `","RepeatMode":"RepeatForever"}`},
		{"", `{"ItemId":"` + id + `","PositionTicks":"abc"}`},
		{"/Progress", `{"ItemId":"not-a-guid"}`},
		{"/Stopped", `{"ItemId":"not-a-guid"}`},
		{"/Stopped", `{"ItemId":"` + id + `","PositionTicks":-5}`},
	}
	for _, c := range bad {
		if code := e.report(e.aliceTok, c.kind, c.body); code != 400 {
			t.Errorf("POST /Sessions/Playing%s %s = %d, want 400", c.kind, c.body, code)
		}
	}
	good := []string{`{}`, `{"ItemId":"00000000-0000-0000-0000-000000000000"}`, `{"ItemId":"7f3c1d2e-9a4b-4c5d-8e6f-0a1b2c3d4e5f"}`,
		`{"ItemId":"` + id + `","Brightness":50,"AspectRatio":"16:9","SessionId":"x","Extra":{"a":1}}`}
	for _, b := range good {
		for _, k := range []string{"", "/Progress", "/Stopped"} {
			if code := e.report(e.aliceTok, k, b); code != 204 {
				t.Errorf("POST /Sessions/Playing%s %s = %d, want 204", k, b, code)
			}
		}
	}
	// unknown items touched nothing
	if st, _ := e.st.GetPlaystate(context.Background(), e.alice.ID, "7f3c1d2e-9a4b-4c5d-8e6f-0a1b2c3d4e5f"); st.PlayCount != 0 {
		t.Fatalf("unknown item data written: %+v", st)
	}
}

func TestSessionPlayState(t *testing.T) {
	e := newPSEnv(t)
	dev := "dev-" + e.aliceTok[:6]
	read := func() map[string]any {
		code, b := e.do(e.adminTok, http.MethodGet, "/Sessions?deviceId="+dev, "")
		wantStatus(t, code, 200, "sessions")
		var list []map[string]any
		if err := json.Unmarshal(b, &list); err != nil || len(list) != 1 {
			t.Fatalf("sessions = %s (%v)", b, err)
		}
		return list[0]
	}
	id := string(e.movie)
	e.report(e.aliceTok, "", `{"ItemId":"`+id+`","PlayMethod":"Transcode","IsPaused":true,"IsMuted":true,"VolumeLevel":42,"RepeatMode":"RepeatAll","PlaybackOrder":"Shuffle","CanSeek":true,"PositionTicks":1000000,"NowPlayingQueue":[{"Id":"aaaa","PlaylistItemId":"p0"}]}`)
	s := read()
	np, _ := s["NowPlayingItem"].(map[string]any)
	if np["Id"] != id {
		t.Fatalf("NowPlayingItem = %v", s["NowPlayingItem"])
	}
	ps := s["PlayState"].(map[string]any)
	if ps["IsPaused"] != true || ps["IsMuted"] != true || num(ps, "VolumeLevel") != 42 || ps["RepeatMode"] != "RepeatAll" ||
		ps["PlaybackOrder"] != "Shuffle" || ps["CanSeek"] != true || num(ps, "PositionTicks") != 1000000 ||
		ps["PlayMethod"] != "DirectPlay" || ps["MediaSourceId"] != id {
		t.Fatalf("PlayState = %v", ps)
	}
	if q, _ := s["NowPlayingQueue"].([]any); len(q) != 0 {
		t.Fatalf("queue stored by start: %v", s["NowPlayingQueue"])
	}
	// progress replaces the state; omitted values fall back to defaults
	e.report(e.aliceTok, "/Progress", `{"ItemId":"`+id+`","PlayMethod":"DirectStream","IsPaused":true,"PositionTicks":100000}`)
	ps = read()["PlayState"].(map[string]any)
	if ps["IsMuted"] != false || ps["RepeatMode"] != "RepeatNone" || ps["PlaybackOrder"] != "Default" || ps["PlayMethod"] != "DirectStream" {
		t.Fatalf("PlayState after progress = %v", ps)
	}
	// stopped clears now playing and stores a non-null queue
	e.report(e.aliceTok, "/Stopped", `{"ItemId":"`+id+`","Failed":true,"NowPlayingQueue":[{"Id":"bbbb","PlaylistItemId":"p1"}]}`)
	s = read()
	if _, has := s["NowPlayingItem"]; has {
		t.Fatalf("NowPlayingItem after stop: %v", s["NowPlayingItem"])
	}
	q, _ := s["NowPlayingQueue"].([]any)
	if len(q) != 1 || q[0].(map[string]any)["Id"] != "bbbb" {
		t.Fatalf("queue after stop = %v", s["NowPlayingQueue"])
	}
	// ping: 204 with an id, 400 without, no session effect
	code, _ := e.do(e.aliceTok, http.MethodPost, "/Sessions/Playing/Ping?playSessionId=x", "")
	wantStatus(t, code, 204, "ping")
	code, _ = e.do(e.aliceTok, http.MethodPost, "/Sessions/Playing/Ping", "")
	wantStatus(t, code, 400, "ping without id")
}
