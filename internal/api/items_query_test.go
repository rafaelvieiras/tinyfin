package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// itemsFixture is a miniature of docs/contract/fixtures.json: two libraries
// (Movies with a library-root Folder item and three movies; Shows with one
// series, two seasons and three episodes), plus an administrator, a default
// user and a user without access to any library.
type itemsFixture struct {
	ts                                    string
	st                                    storage.Store
	admin, alice, nofolders               storage.User
	adminTok, aliceTok, nofoldersTok      string
	movies, shows                         storage.LibraryID
	rootFolder                            storage.ItemID
	alpha, bravo, charlie                 storage.ItemID
	show, season1, season2, e11, e12, e21 storage.ItemID
}

func newItemsFixture(t *testing.T) *itemsFixture {
	t.Helper()
	ts, st := newTestServer(t)
	f := &itemsFixture{ts: ts.URL, st: st}
	ctx := context.Background()

	f.admin = createTestAdminUser(t, st, "admin", "adminpass")
	f.alice = createTestUser(t, st, "alice", "alicepass")
	f.nofolders = createTestUser(t, st, "nofolders", "nofolderspass")
	f.nofolders.Policy = `{"EnableAllFolders":false,"EnabledFolders":[]}`
	if err := st.UpdateUser(ctx, f.nofolders); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	f.adminTok = loginToken(t, f.ts, "admin", "adminpass")
	f.aliceTok = loginToken(t, f.ts, "alice", "alicepass")
	f.nofoldersTok = loginToken(t, f.ts, "nofolders", "nofolderspass")

	f.movies = createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	f.shows = createTestLibrary(t, st, "Shows", storage.CollectionTypeTVShows)

	put := func(it storage.Item) storage.ItemID {
		t.Helper()
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatalf("UpsertItem(%q): %v", it.Name, err)
		}
		return it.ID
	}
	w, h := 320, 240
	video := []storage.MediaStreamRef{{Type: storage.StreamTypeVideo, Codec: "h264", Width: &w, Height: &h}}
	ticks := int64(50_000_000)
	year := func(y int) *int { return &y }

	f.rootFolder = put(storage.Item{ID: gid("folder-movies"), Type: storage.ItemTypeFolder, LibraryID: f.movies, Name: "Movies", SortName: "Movies", Path: "/media/Movies"})
	f.alpha = put(storage.Item{ID: gid("alpha"), Type: storage.ItemTypeMovie, LibraryID: f.movies, Name: "Alpha Movie", SortName: "Alpha Movie", ProductionYear: year(2015), Path: "/media/Movies/Alpha/a.mp4", MediaStreams: video, RunTimeTicks: &ticks})
	f.bravo = put(storage.Item{ID: gid("bravo"), Type: storage.ItemTypeMovie, LibraryID: f.movies, Name: "Bravo Movie", SortName: "Bravo Movie", ProductionYear: year(2020), Path: "/media/Movies/Bravo/b.mp4", MediaStreams: video, RunTimeTicks: &ticks})
	f.charlie = put(storage.Item{ID: gid("charlie"), Type: storage.ItemTypeMovie, LibraryID: f.movies, Name: "Charlie Movie", SortName: "Charlie Movie", ProductionYear: year(2018), Path: "/media/Movies/Charlie/c.mp4", MediaStreams: video, RunTimeTicks: &ticks})

	f.show = put(storage.Item{ID: gid("show"), Type: storage.ItemTypeSeries, LibraryID: f.shows, Name: "Test Show", SortName: "Test Show", ProductionYear: year(2019), Path: "/media/Shows/Test Show"})
	s := f.show
	f.season1 = put(storage.Item{ID: gid("season1"), Type: storage.ItemTypeSeason, LibraryID: f.shows, ParentID: &s, SeriesID: &s, Name: "Season 1", SortName: "0001", IndexNumber: intPtr(1)})
	f.season2 = put(storage.Item{ID: gid("season2"), Type: storage.ItemTypeSeason, LibraryID: f.shows, ParentID: &s, SeriesID: &s, Name: "Season 2", SortName: "0002", IndexNumber: intPtr(2)})
	s1, s2 := f.season1, f.season2
	f.e11 = put(storage.Item{ID: gid("e11"), Type: storage.ItemTypeEpisode, LibraryID: f.shows, ParentID: &s1, SeriesID: &s, SeasonID: &s1, Name: "Episode 1", SortName: "Episode 1", IndexNumber: intPtr(1), ParentIndexNumber: intPtr(1), MediaStreams: video})
	f.e12 = put(storage.Item{ID: gid("e12"), Type: storage.ItemTypeEpisode, LibraryID: f.shows, ParentID: &s1, SeriesID: &s, SeasonID: &s1, Name: "Episode 2", SortName: "Episode 2", IndexNumber: intPtr(2), ParentIndexNumber: intPtr(1), MediaStreams: video})
	f.e21 = put(storage.Item{ID: gid("e21"), Type: storage.ItemTypeEpisode, LibraryID: f.shows, ParentID: &s2, SeriesID: &s, SeasonID: &s2, Name: "Episode 1", SortName: "Episode 1", IndexNumber: intPtr(1), ParentIndexNumber: intPtr(2), MediaStreams: video})
	return f
}

// gid derives a stable GUID-shaped item id from a short fixture name.
func gid(name string) storage.ItemID {
	sum := sha256.Sum256([]byte("items-query-test-" + name))
	return storage.ItemID(hex.EncodeToString(sum[:16]))
}

var fixtureNameRE = regexp.MustCompile(`\b(folder-movies|alpha|bravo|charlie|show|season1|season2|e11|e12|e21)\b`)

// resolveNames replaces short fixture names in s with their GUIDs.
func resolveNames(s string) string {
	return fixtureNameRE.ReplaceAllStringFunc(s, func(n string) string { return string(gid(n)) })
}

func resolveAll(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = resolveNames(v)
	}
	return out
}

type itemsReply struct {
	status int
	raw    map[string]json.RawMessage
	Items  []baseItemDto
	Total  int
	Start  int
}

func (f *itemsFixture) get(t *testing.T, token, path string) itemsReply {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts+resolveNames(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := itemsReply{status: resp.StatusCode}
	var body struct {
		Items            []baseItemDto
		TotalRecordCount int
		StartIndex       int
	}
	if resp.StatusCode == http.StatusOK {
		var raw map[string]json.RawMessage
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&raw); err == nil {
			out.raw = raw
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &body)
		}
	}
	out.Items, out.Total, out.Start = body.Items, body.TotalRecordCount, body.StartIndex
	return out
}

func ids(r itemsReply) []string {
	out := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		out = append(out, it.ID)
	}
	return out
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[string]int{}
	for _, g := range got {
		m[g]++
	}
	for _, w := range want {
		m[w]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestItemsList_Queries(t *testing.T) {
	f := newItemsFixture(t)
	q := func(kv ...string) string {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], resolveNames(kv[i+1]))
		}
		return "/Items?" + v.Encode()
	}
	type want struct {
		status  int
		total   int // -1: unchecked
		ids     []string
		ordered bool
	}
	movies3 := []string{"alpha", "bravo", "charlie"}
	cases := []struct {
		name  string
		token *string
		path  string
		want  want
	}{
		{"recursive movies", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 3, movies3, false}},
		{"types alone imply recursion", &f.aliceTok, q("IncludeItemTypes", "Movie"), want{200, 3, movies3, false}},
		{"name filter alone implies recursion", &f.aliceTok, q("NameStartsWith", "Bravo"), want{200, 1, []string{"bravo"}, false}},
		{"library direct children", &f.adminTok, q("ParentId", string(f.movies)), want{200, 3, movies3, false}},
		{"library recursive includes root folder", &f.adminTok, q("ParentId", string(f.movies), "Recursive", "true"), want{200, 4, append([]string{"folder-movies"}, movies3...), false}},
		{"library with types defaults to recursive", &f.adminTok, q("ParentId", string(f.shows), "IncludeItemTypes", "Episode"), want{200, 3, []string{"e11", "e12", "e21"}, false}},
		{"series non-recursive ignores types", &f.adminTok, q("ParentId", "show", "IncludeItemTypes", "Episode"), want{200, 2, []string{"season1", "season2"}, false}},
		{"series recursive", &f.adminTok, q("ParentId", "show", "Recursive", "true"), want{200, 5, []string{"season1", "season2", "e11", "e12", "e21"}, false}},
		{"season children", &f.adminTok, q("ParentId", "season1"), want{200, 2, []string{"e11", "e12"}, false}},
		{"unknown parent", &f.adminTok, q("ParentId", "11111111-2222-4333-8444-555555555555"), want{400, -1, nil, false}},
		{"zero parent", &f.adminTok, q("ParentId", "00000000-0000-0000-0000-000000000000"), want{400, -1, nil, false}},
		{"forbidden library", &f.nofoldersTok, q("ParentId", string(f.movies)), want{401, -1, nil, false}},
		{"ids", &f.adminTok, q("Ids", "charlie,alpha"), want{200, 2, []string{"charlie", "alpha"}, true}},
		{"ids order overridden by SortBy", &f.adminTok, q("Ids", "charlie,alpha", "SortBy", "SortName"), want{200, 2, []string{"alpha", "charlie"}, true}},
		{"ids restricted", &f.nofoldersTok, q("Ids", "alpha,bravo"), want{200, 0, nil, false}},
		{"ids drop garbage", &f.adminTok, q("Ids", "alpha,not-a-guid"), want{200, 1, []string{"alpha"}, false}},
		{"ids with type filter", &f.adminTok, q("Ids", "alpha,e11", "IncludeItemTypes", "Movie"), want{200, 1, []string{"alpha"}, false}},
		{"unknown include type only", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "NotAType"), want{200, 10, nil, false}},
		{"exclude types", &f.adminTok, q("Recursive", "true", "ExcludeItemTypes", "Movie"), want{200, 7, nil, false}},
		{"exclude ignored with include", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "ExcludeItemTypes", "Movie"), want{200, 3, movies3, false}},
		{"media type video", &f.adminTok, q("Recursive", "true", "MediaTypes", "Video"), want{200, 6, []string{"alpha", "bravo", "charlie", "e11", "e12", "e21"}, false}},
		{"media type audio", &f.adminTok, q("Recursive", "true", "MediaTypes", "Audio"), want{200, 0, nil, false}},
		{"years", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "Years", "2015,2020"), want{200, 2, []string{"alpha", "bravo"}, false}},
		{"name range", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "NameStartsWithOrGreater", "b", "NameLessThan", "c"), want{200, 1, []string{"bravo"}, false}},
		{"exclude ids", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "ExcludeItemIds", "alpha,bravo"), want{200, 1, []string{"charlie"}, false}},
		{"parent index", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Episode", "ParentIndexNumber", "1"), want{200, 2, []string{"e11", "e12"}, false}},
		{"index", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Episode", "IndexNumber", "1"), want{200, 2, []string{"e11", "e21"}, false}},
		{"legacy alias ignores IndexNumber", &f.aliceTok, "/Users/" + string(f.alice.ID) + "/Items?" + url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Episode"}, "IndexNumber": {"1"}}.Encode(), want{200, 3, nil, false}},
		{"IsFolder", &f.adminTok, q("Recursive", "true", "Filters", "IsFolder"), want{200, 4, []string{"folder-movies", "show", "season1", "season2"}, false}},
		{"IsNotFolder", &f.adminTok, q("Recursive", "true", "Filters", "IsNotFolder"), want{200, 6, nil, false}},
		{"filters conflict", &f.adminTok, q("Recursive", "true", "Filters", "IsFolder,IsNotFolder"), want{400, -1, nil, false}},
		{"filters played conflict", &f.adminTok, q("Recursive", "true", "Filters", "IsPlayed,IsUnplayed"), want{400, -1, nil, false}},
		{"filters likes conflict", &f.adminTok, q("Recursive", "true", "Filters", "Likes,Dislikes"), want{400, -1, nil, false}},
		{"adjacent middle", &f.adminTok, q("AdjacentTo", "bravo", "SortBy", "SortName", "Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 3, movies3, true}},
		{"adjacent first", &f.adminTok, q("AdjacentTo", "alpha", "SortBy", "SortName", "Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 2, []string{"alpha", "bravo"}, true}},
		{"adjacent unknown", &f.adminTok, q("AdjacentTo", "nope", "SortBy", "SortName", "Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 0, nil, false}},
		{"IsHd false", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "IsHd", "false"), want{200, 3, movies3, false}},
		{"IsHd true", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "IsHd", "true"), want{200, 0, nil, false}},
		{"Is4K false alone", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "Is4K", "false"), want{200, 0, nil, false}},
		{"MinWidth", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "MinWidth", "400"), want{200, 0, nil, false}},
		{"MaxHeight", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "MaxHeight", "240"), want{200, 3, movies3, false}},
		{"genres none", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "Genres", "Nope"), want{200, 0, nil, false}},
		{"HasOverview false", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "HasOverview", "false"), want{200, 3, movies3, false}},
		{"HasOverview true", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "HasOverview", "true"), want{200, 0, nil, false}},
		{"ImageTypes none", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "ImageTypes", "Backdrop"), want{200, 0, nil, false}},
		{"MinCommunityRating", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "MinCommunityRating", "1"), want{200, 0, nil, false}},
		{"MaxOfficialRating leaves unrated", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "MaxOfficialRating", "PG-13"), want{200, 3, movies3, false}},
		{"search substring", &f.aliceTok, q("SearchTerm", "lph"), want{200, 1, []string{"alpha"}, false}},
		{"search case-insensitive", &f.aliceTok, q("SearchTerm", "ALPHA"), want{200, 1, []string{"alpha"}, false}},
		{"search ranking ignores sort", &f.aliceTok, q("SearchTerm", "movie", "IncludeItemTypes", "Movie", "SortBy", "SortName", "SortOrder", "Descending"), want{200, 3, movies3, true}},
		{"search matches series name for episodes", &f.adminTok, q("SearchTerm", "Test", "IncludeItemTypes", "Episode"), want{200, 3, nil, false}},
		{"search outside parent", &f.adminTok, q("ParentId", string(f.movies), "SearchTerm", "Test", "Recursive", "true"), want{200, 0, nil, false}},
		{"search restricted", &f.nofoldersTok, q("SearchTerm", "Alpha"), want{200, 0, nil, false}},
		{"sort name desc", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortBy", "Name", "SortOrder", "Descending"), want{200, 3, []string{"charlie", "bravo", "alpha"}, true}},
		{"sort production year", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortBy", "ProductionYear"), want{200, 3, []string{"alpha", "charlie", "bravo"}, true}},
		{"sort premiere date falls back to year", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortBy", "PremiereDate"), want{200, 3, []string{"alpha", "charlie", "bravo"}, true}},
		{"sort tie break by name", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortBy", "ParentIndexNumber", "SortOrder", "Ascending"), want{200, 3, movies3, true}},
		{"sort multi key per-key order", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Episode", "SortBy", "ParentIndexNumber,IndexNumber", "SortOrder", "Descending,Ascending"), want{200, 3, []string{"e21", "e11", "e12"}, true}},
		{"surplus sort orders", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Episode", "SortBy", "ParentIndexNumber", "SortOrder", "Ascending,Descending,Descending"), want{400, -1, nil, false}},
		{"sort order without key", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortOrder", "Descending"), want{200, 3, movies3, true}},
		{"unknown sort key", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "SortBy", "NotASortKey", "SortOrder", "Descending,Ascending"), want{200, 3, movies3, true}},
		{"limit zero", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "Limit", "0"), want{200, 3, []string{}, false}},
		{"limit and start", &f.adminTok, q("Recursive", "true", "IncludeItemTypes", "Movie", "Limit", "1", "StartIndex", "1", "SortBy", "SortName"), want{200, 3, []string{"bravo"}, true}},
		{"bad limit", &f.adminTok, q("Limit", "abc"), want{400, -1, nil, false}},
		{"bad start index", &f.adminTok, q("StartIndex", "abc"), want{400, -1, nil, false}},
		{"bad recursive", &f.adminTok, q("Recursive", "maybe"), want{400, -1, nil, false}},
		{"no libraries recursive", &f.nofoldersTok, q("Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 0, nil, false}},
		{"other user forbidden", &f.aliceTok, q("UserId", string(f.admin.ID)), want{403, -1, nil, false}},
		{"unknown user for non-admin", &f.aliceTok, q("UserId", "11111111-2222-4333-8444-555555555555"), want{403, -1, nil, false}},
		{"unknown user for admin", &f.adminTok, q("UserId", "11111111-2222-4333-8444-555555555555"), want{404, -1, nil, false}},
		{"malformed user", &f.adminTok, q("UserId", "not-a-guid"), want{400, -1, nil, false}},
		{"admin acts as restricted user", &f.adminTok, q("UserId", string(f.nofolders.ID), "Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 0, nil, false}},
		{"zero user id is the caller", &f.nofoldersTok, q("UserId", "00000000-0000-0000-0000-000000000000", "Recursive", "true", "IncludeItemTypes", "Movie"), want{200, 0, nil, false}},
		{"anonymous", new(string), q("Recursive", "true"), want{401, -1, nil, false}},
		{"non-folder parent, no filters: views", &f.adminTok, q("ParentId", "alpha"), want{200, 2, nil, false}},
		{"non-folder parent, types", &f.adminTok, q("ParentId", "alpha", "IncludeItemTypes", "Movie"), want{200, 3, movies3, false}},
		{"non-folder parent, restricted", &f.nofoldersTok, q("ParentId", "alpha", "Recursive", "true"), want{200, 0, nil, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.get(t, *tc.token, tc.path)
			if r.status != tc.want.status {
				t.Fatalf("status = %d, want %d (%s)", r.status, tc.want.status, tc.path)
			}
			if tc.want.status != 200 {
				return
			}
			if tc.want.total >= 0 && r.Total != tc.want.total {
				t.Errorf("TotalRecordCount = %d, want %d (items %v)", r.Total, tc.want.total, ids(r))
			}
			if tc.want.ids != nil {
				got, wantIDs := ids(r), resolveAll(tc.want.ids)
				if tc.want.ordered {
					if strings.Join(got, ",") != strings.Join(wantIDs, ",") {
						t.Errorf("ids = %v, want ordered %v", got, wantIDs)
					}
				} else if !sameSet(got, wantIDs) {
					t.Errorf("ids = %v, want %v", got, wantIDs)
				}
			}
		})
	}
}

func TestItemsList_RootViews(t *testing.T) {
	f := newItemsFixture(t)

	r := f.get(t, f.aliceTok, "/Items")
	if r.status != 200 || len(r.Items) != 3 || r.Total != 3 {
		t.Fatalf("root views: status %d items %d total %d, want 200/3/3", r.status, len(r.Items), r.Total)
	}
	last := r.Items[2]
	if last.Type != "ManualPlaylistsFolder" || last.CollectionType != "playlists" || !last.IsFolder {
		t.Errorf("last view = %+v, want the Playlists folder", last)
	}

	// Paging, sorting and shaping parameters do not turn the request into a
	// query, are ignored, and StartIndex is only echoed.
	r = f.get(t, f.aliceTok, "/Items?Limit=1&StartIndex=1&SortBy=SortName&SortOrder=Descending&Fields=Path")
	if len(r.Items) != 3 || r.Total != 3 || r.Start != 1 {
		t.Errorf("root views with paging: items %d total %d start %d, want 3/3/1", len(r.Items), r.Total, r.Start)
	}

	// A user with no library access still gets the Playlists folder.
	r = f.get(t, f.nofoldersTok, "/Items")
	if len(r.Items) != 1 || r.Items[0].Type != "ManualPlaylistsFolder" {
		t.Errorf("restricted root views = %v, want only the Playlists folder", ids(r))
	}

	// GET /UserViews respects the library restriction and has no Playlists.
	r = f.get(t, f.nofoldersTok, "/UserViews")
	if r.status != 200 || len(r.Items) != 0 {
		t.Errorf("restricted /UserViews = %d items (status %d), want 0", len(r.Items), r.status)
	}
	r = f.get(t, f.aliceTok, "/UserViews")
	if len(r.Items) != 2 {
		t.Errorf("/UserViews = %d items, want 2", len(r.Items))
	}
}

func TestItemsList_TotalRecordCountAndFields(t *testing.T) {
	f := newItemsFixture(t)

	r := f.get(t, f.adminTok, "/Items?Recursive=true&IncludeItemTypes=Movie&EnableTotalRecordCount=false&Limit=2&SortBy=SortName")
	if len(r.Items) != 2 || r.Total != 2 {
		t.Errorf("EnableTotalRecordCount=false: items %d total %d, want 2/2", len(r.Items), r.Total)
	}

	// Path is only present when requested through Fields.
	r = f.get(t, f.adminTok, "/Items?Ids=alpha")
	if len(r.Items) != 1 || r.Items[0].Path != "" {
		t.Errorf("without Fields: %+v, want no Path", r.Items)
	}
	r = f.get(t, f.adminTok, "/Items?Ids=alpha&Fields=Path,NotAField")
	if len(r.Items) != 1 || r.Items[0].Path == "" {
		t.Errorf("Fields=Path: %+v, want a Path", r.Items)
	}
	r = f.get(t, f.adminTok, "/Items?Ids=alpha&Fields=NotAField")
	if len(r.Items) != 1 || r.Items[0].Path != "" {
		t.Errorf("Fields=NotAField: %+v, want no Path", r.Items)
	}

	// UserData is present by default and omitted by EnableUserData=false.
	r = f.get(t, f.aliceTok, "/Items?Ids=alpha")
	if r.Items[0].UserData == nil {
		t.Error("default listing has no UserData")
	}
	r = f.get(t, f.aliceTok, "/Items?Ids=alpha&EnableUserData=false")
	if r.Items[0].UserData != nil {
		t.Error("EnableUserData=false still has UserData")
	}

	// Movie shape.
	r = f.get(t, f.adminTok, "/Items?Ids=alpha")
	it := r.Items[0]
	if it.Type != "Movie" || it.IsFolder || it.MediaType != "Video" || it.ProductionYear == nil || *it.ProductionYear != 2015 || it.RunTimeTicks == nil || it.ServerID == "" {
		t.Errorf("movie dto = %+v", it)
	}
}

func TestItemsList_PlayedFavoriteResumable(t *testing.T) {
	f := newItemsFixture(t)
	ctx := context.Background()
	set := func(item storage.ItemID, st storage.Playstate) {
		t.Helper()
		if err := f.st.SetPlaystate(ctx, f.alice.ID, item, st); err != nil {
			t.Fatalf("SetPlaystate: %v", err)
		}
	}
	set(f.alpha, storage.Playstate{Played: true})
	set(f.bravo, storage.Playstate{IsFavorite: true})
	set(f.charlie, storage.Playstate{PlaybackPositionTicks: 20_000_000})

	check := func(name, token, path string, want ...string) {
		t.Helper()
		r := f.get(t, token, path)
		if r.status != 200 || !sameSet(ids(r), resolveAll(want)) {
			t.Errorf("%s: status %d ids %v, want %v", name, r.status, ids(r), want)
		}
	}
	base := "&Recursive=true&IncludeItemTypes=Movie"
	check("IsPlayed filter", f.aliceTok, "/Items?Filters=IsPlayed"+base, "alpha")
	check("IsUnplayed filter", f.aliceTok, "/Items?Filters=IsUnplayed"+base, "bravo", "charlie")
	check("IsPlayed=true", f.aliceTok, "/Items?IsPlayed=true"+base, "alpha")
	check("IsPlayed=false", f.aliceTok, "/Items?IsPlayed=false"+base, "bravo", "charlie")
	check("other user has nothing played", f.adminTok, "/Items?Filters=IsPlayed"+base)
	check("IsFavorite filter", f.aliceTok, "/Items?Filters=IsFavorite"+base, "bravo")
	check("IsFavorite=true", f.aliceTok, "/Items?IsFavorite=true"+base, "bravo")
	check("IsFavorite=false", f.aliceTok, "/Items?IsFavorite=false"+base, "alpha", "charlie")
	check("IsResumable", f.aliceTok, "/Items?Filters=IsResumable"+base, "charlie")
	check("admin sees none resumable", f.adminTok, "/Items?Filters=IsResumable"+base)

	// A folder is played only when every leaf below it is.
	set(f.e11, storage.Playstate{Played: true})
	set(f.e12, storage.Playstate{Played: true})
	check("series partially played", f.aliceTok, "/Items?Filters=IsPlayed&Recursive=true&IncludeItemTypes=Series")
	check("season 1 fully played", f.aliceTok, "/Items?Filters=IsPlayed&Recursive=true&IncludeItemTypes=Season", "season1")
	set(f.e21, storage.Playstate{Played: true})
	check("series fully played", f.aliceTok, "/Items?Filters=IsPlayed&Recursive=true&IncludeItemTypes=Series", "show")
	check("series no longer unplayed", f.aliceTok, "/Items?Filters=IsUnplayed&Recursive=true&IncludeItemTypes=Series")
}

func TestItemGet_VisibilityAndRoot(t *testing.T) {
	f := newItemsFixture(t)
	problem := func(path, token string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, f.ts+resolveNames(path), nil)
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("%s: status %d, want 404", path, resp.StatusCode)
		}
		var p map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatalf("%s: 404 body is not JSON: %v", path, err)
		}
		if p["title"] == nil || p["type"] == nil || p["status"] != float64(404) {
			t.Errorf("%s: problem body = %v", path, p)
		}
	}
	problem("/Items/11111111-2222-4333-8444-555555555555", f.adminTok)
	problem("/Items/alpha", f.nofoldersTok)
	problem("/Items/show", f.nofoldersTok)
	problem("/Items/alpha?UserId="+string(f.nofolders.ID), f.adminTok)
	problem("/Users/"+string(f.nofolders.ID)+"/Items/alpha", f.nofoldersTok)

	status := func(path, token string) int {
		t.Helper()
		return f.get(t, token, path).status
	}
	if got := status("/Items/alpha", f.aliceTok); got != 200 {
		t.Errorf("alice GET alpha = %d", got)
	}
	if got := status("/Items/alpha?UserId="+string(f.admin.ID), f.aliceTok); got != 403 {
		t.Errorf("alice as admin = %d, want 403", got)
	}
	if got := status("/Items/alpha?UserId=11111111-2222-4333-8444-555555555555", f.aliceTok); got != 403 {
		t.Errorf("alice unknown user = %d, want 403", got)
	}
	if got := status("/Items/alpha?UserId=11111111-2222-4333-8444-555555555555", f.adminTok); got != 404 {
		t.Errorf("admin unknown user = %d, want 404", got)
	}
	if got := status("/Items/alpha?UserId=00000000-0000-0000-0000-000000000000", f.aliceTok); got != 200 {
		t.Errorf("zero user = %d, want 200", got)
	}
	if got := status("/Items/alpha", ""); got != 401 {
		t.Errorf("anonymous = %d, want 401", got)
	}
	if got := status("/Users/"+string(f.admin.ID)+"/Items/alpha", f.aliceTok); got != 403 {
		t.Errorf("legacy alice as admin = %d, want 403", got)
	}
	if got := status("/Users/"+string(f.alice.ID)+"/Items/alpha", f.adminTok); got != 200 {
		t.Errorf("legacy admin as alice = %d, want 200", got)
	}
	if got := status("/Users/11111111-2222-4333-8444-555555555555/Items/alpha", f.adminTok); got != 404 {
		t.Errorf("legacy admin unknown user = %d, want 404", got)
	}

	// The all-zero id is the user's root folder, also without libraries.
	for _, tok := range []string{f.adminTok, f.nofoldersTok} {
		req, _ := http.NewRequest(http.MethodGet, f.ts+"/Items/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", `MediaBrowser Token="`+tok+`"`)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var dto baseItemDto
		_ = json.NewDecoder(resp.Body).Decode(&dto)
		resp.Body.Close()
		if resp.StatusCode != 200 || !dto.IsFolder || dto.ID == "" {
			t.Errorf("root: status %d dto %+v", resp.StatusCode, dto)
		}
	}

	// A single read is always the full object: Path, UserData.
	req, _ := http.NewRequest(http.MethodGet, f.ts+resolveNames("/Items/alpha?EnableUserData=false&Fields=Overview"), nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+f.aliceTok+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var dto baseItemDto
	_ = json.NewDecoder(resp.Body).Decode(&dto)
	if dto.Path == "" || dto.UserData == nil {
		t.Errorf("single read lost Path/UserData: %+v", dto)
	}
}

func TestItemsList_BlockedTagsHideItems(t *testing.T) {
	f := newItemsFixture(t)
	ctx := context.Background()
	it, err := f.st.GetItem(ctx, f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	it.Tags = []string{"adult"}
	if err := f.st.UpsertItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	f.alice.Policy = `{"BlockedTags":["Adult"]}`
	if err := f.st.UpdateUser(ctx, f.alice); err != nil {
		t.Fatal(err)
	}
	r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Movie")
	if r.Total != 2 || sameSet(ids(r), []string{string(f.alpha)}) {
		t.Errorf("blocked tag listing = %v (total %d)", ids(r), r.Total)
	}
	if got := f.get(t, f.aliceTok, "/Items/alpha").status; got != 404 {
		t.Errorf("blocked item GET = %d, want 404", got)
	}
}
