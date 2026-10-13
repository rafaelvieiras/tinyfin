package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// ratingFixture extends itemsFixture with rated movies and a rated series
// (items.rating.R*). Names are the movie names used in assertions.
type ratingFixture struct {
	*itemsFixture
	movie                    map[string]storage.ItemID // by rating text
	series                   storage.ItemID
	epInherit, epOwn, season storage.ItemID
	alphaPlain               storage.ItemID
}

func newRatingFixture(t *testing.T) *ratingFixture {
	t.Helper()
	f := &ratingFixture{itemsFixture: newItemsFixture(t), movie: map[string]storage.ItemID{}}
	ctx := context.Background()
	w, h := 320, 240
	video := []storage.MediaStreamRef{{Type: storage.StreamTypeVideo, Codec: "h264", Width: &w, Height: &h}}
	ticks := int64(50_000_000)
	for _, r := range []string{"G", "PG", "PG-13", "R", "NC-17", "TV-MA", "12", "ZZ-99", "ABC"} {
		id := gid("rated-" + r)
		it := storage.Item{ID: id, Type: storage.ItemTypeMovie, LibraryID: f.movies, Name: "Rated " + r, SortName: "Rated " + r,
			Path: "/media/Movies/" + r + ".mp4", MediaStreams: video, RunTimeTicks: &ticks, OfficialRating: r}
		if err := f.st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
		f.movie[r] = id
	}
	f.alphaPlain = f.alpha
	s := gid("rated-series")
	f.series = s
	put := func(it storage.Item) {
		t.Helper()
		if err := f.st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	put(storage.Item{ID: s, Type: storage.ItemTypeSeries, LibraryID: f.shows, Name: "Rated Show", SortName: "Rated Show", Path: "/media/Shows/Rated Show", OfficialRating: "TV-MA"})
	f.season = gid("rated-season")
	put(storage.Item{ID: f.season, Type: storage.ItemTypeSeason, LibraryID: f.shows, ParentID: &s, SeriesID: &s, Name: "Season 1", SortName: "0001", IndexNumber: intPtr(1)})
	season := f.season
	f.epInherit = gid("rated-ep-inherit")
	put(storage.Item{ID: f.epInherit, Type: storage.ItemTypeEpisode, LibraryID: f.shows, ParentID: &season, SeriesID: &s, SeasonID: &season, Name: "Inheriting", SortName: "Inheriting", IndexNumber: intPtr(1), ParentIndexNumber: intPtr(1), MediaStreams: video})
	f.epOwn = gid("rated-ep-own")
	put(storage.Item{ID: f.epOwn, Type: storage.ItemTypeEpisode, LibraryID: f.shows, ParentID: &season, SeriesID: &s, SeasonID: &season, Name: "Own rating", SortName: "Own rating", IndexNumber: intPtr(2), ParentIndexNumber: intPtr(1), MediaStreams: video, OfficialRating: "TV-Y"})
	return f
}

func (f *ratingFixture) setPolicy(t *testing.T, policy string) {
	t.Helper()
	f.alice.Policy = policy
	if err := f.st.UpdateUser(context.Background(), f.alice); err != nil {
		t.Fatal(err)
	}
}

func (f *ratingFixture) movieNames(t *testing.T, query string) []string {
	t.Helper()
	r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Movie"+query)
	if r.status != 200 {
		t.Fatalf("list %s: %d", query, r.status)
	}
	var out []string
	for _, it := range r.Items {
		out = append(out, it.Name)
	}
	return out
}

func containsAll(got []string, want ...string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func containsAny(got []string, any ...string) bool {
	for _, g := range got {
		for _, a := range any {
			if g == a {
				return true
			}
		}
	}
	return false
}

// items.rating: MaxParentalRating hides rated items above it, keeps unrated
// ones; the sub rating breaks ties at the same value.
func TestRatingPolicyMax(t *testing.T) {
	f := newRatingFixture(t)

	f.setPolicy(t, `{"MaxParentalRating":10}`)
	got := f.movieNames(t, "")
	if !containsAll(got, "Rated G", "Rated PG", "Alpha Movie", "Bravo Movie") || containsAny(got, "Rated PG-13", "Rated R", "Rated 12", "Rated ZZ-99") {
		t.Errorf("max 10: %v", got)
	}
	// An unparsable rating counts as unrated and passes the maximum.
	if !containsAll(got, "Rated ABC") {
		t.Errorf("max 10 should keep the unrecognized rating: %v", got)
	}
	f.setPolicy(t, `{"MaxParentalRating":13}`)
	got = f.movieNames(t, "")
	if !containsAll(got, "Rated PG-13", "Rated 12") || containsAny(got, "Rated R", "Rated ZZ-99") {
		t.Errorf("max 13: %v", got)
	}
	// R is 17/0; NC-17 and TV-MA are 17/1 and need the sub rating.
	f.setPolicy(t, `{"MaxParentalRating":17}`)
	got = f.movieNames(t, "")
	if !containsAll(got, "Rated R") || containsAny(got, "Rated NC-17", "Rated TV-MA") {
		t.Errorf("max 17: %v", got)
	}
	f.setPolicy(t, `{"MaxParentalRating":17,"MaxParentalSubRating":1}`)
	got = f.movieNames(t, "")
	if !containsAll(got, "Rated R", "Rated NC-17", "Rated TV-MA") {
		t.Errorf("max 17 sub 1: %v", got)
	}
	f.setPolicy(t, `{}`)
	if got = f.movieNames(t, ""); !containsAll(got, "Rated ZZ-99", "Rated NC-17") {
		t.Errorf("no maximum: %v", got)
	}
}

func TestRatingBlockUnrated(t *testing.T) {
	f := newRatingFixture(t)
	f.setPolicy(t, `{"BlockUnratedItems":["Movie"]}`)
	got := f.movieNames(t, "")
	if containsAny(got, "Alpha Movie", "Rated ABC") || !containsAll(got, "Rated G", "Rated ZZ-99") {
		t.Errorf("block unrated Movie: %v", got)
	}
	if code := f.get(t, f.aliceTok, "/Items/alpha").status; code != 404 {
		t.Errorf("unrated movie GET = %d, want 404", code)
	}
	if code := f.get(t, f.aliceTok, "/Items/"+string(f.movie["G"])).status; code != 200 {
		t.Errorf("rated movie GET = %d, want 200", code)
	}
	// The Series group covers seasons and episodes; an unrated one is blocked,
	// one that inherits the series' rating is not.
	f.setPolicy(t, `{"BlockUnratedItems":["Series"]}`)
	r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Series,Season,Episode")
	names := []string{}
	for _, it := range r.Items {
		names = append(names, it.Name)
	}
	if containsAny(names, "Test Show", "Episode 1") || !containsAll(names, "Rated Show", "Season 1", "Inheriting", "Own rating") {
		t.Errorf("block unrated Series: %v", names)
	}
}

func TestRatingQueryMaxMin(t *testing.T) {
	f := newRatingFixture(t)
	got := f.movieNames(t, "&MaxOfficialRating=PG")
	if !containsAll(got, "Rated G", "Rated PG", "Alpha Movie") || containsAny(got, "Rated PG-13", "Rated R") {
		t.Errorf("MaxOfficialRating=PG: %v", got)
	}
	// The sub rating counts here too: R excludes NC-17 and TV-MA.
	got = f.movieNames(t, "&MaxOfficialRating=R")
	if !containsAll(got, "Rated R") || containsAny(got, "Rated NC-17", "Rated TV-MA") {
		t.Errorf("MaxOfficialRating=R: %v", got)
	}
	// Numeric and unknown values.
	if got = f.movieNames(t, "&MaxOfficialRating=12"); !containsAll(got, "Rated PG", "Rated 12") || containsAny(got, "Rated PG-13") {
		t.Errorf("MaxOfficialRating=12: %v", got)
	}
	if got = f.movieNames(t, "&MaxOfficialRating=Unrated"); !containsAll(got, "Rated R", "Rated ZZ-99") {
		t.Errorf("MaxOfficialRating=Unrated filters nothing: %v", got)
	}
	got = f.movieNames(t, "&MinOfficialRating=R")
	if containsAny(got, "Rated G", "Rated PG", "Rated PG-13") || !containsAll(got, "Rated R", "Rated NC-17", "Rated ZZ-99", "Alpha Movie") {
		t.Errorf("MinOfficialRating=R keeps unrated and rated >= R: %v", got)
	}
	got = f.movieNames(t, "&MinOfficialRating=PG&MaxOfficialRating=R")
	if containsAny(got, "Rated G", "Rated NC-17") || !containsAll(got, "Rated PG", "Rated PG-13", "Rated R") {
		t.Errorf("Min PG Max R: %v", got)
	}
}

// Single-item reads compare the value only (the sub rating is ignored) and
// also require the series' rating to fit.
func TestRatingSingleItemGate(t *testing.T) {
	f := newRatingFixture(t)
	status := func(id storage.ItemID) int { return f.get(t, f.aliceTok, "/Items/"+string(id)).status }

	f.setPolicy(t, `{"MaxParentalRating":10}`)
	if status(f.movie["G"]) != 200 || status(f.movie["R"]) != 404 {
		t.Error("movie gate at max 10")
	}
	// Under max 10 the inheriting episode, the one with its own TV-Y rating
	// (the series is TV-MA), the season and the series are all refused...
	for name, id := range map[string]storage.ItemID{"series": f.series, "season": f.season, "inheriting": f.epInherit, "own": f.epOwn} {
		if status(id) != 404 {
			t.Errorf("%s at max 10: want 404", name)
		}
	}
	// ...yet the list filter looks at the item's own rating only.
	r := f.get(t, f.aliceTok, "/Items?Recursive=true&IncludeItemTypes=Episode&NameStartsWith=001%20-%200002%20-%20Own") // episode sort names are season - episode - title (items.sort.R8)
	if len(r.Items) != 1 {
		t.Errorf("own-rated episode in the listing at max 10: %d", len(r.Items))
	}
	f.setPolicy(t, `{"MaxParentalRating":17}`)
	for name, id := range map[string]storage.ItemID{"nc17": f.movie["NC-17"], "tvma": f.movie["TV-MA"], "series": f.series, "inheriting": f.epInherit, "own": f.epOwn} {
		if status(id) != 200 {
			t.Errorf("%s at max 17: want 200 (sub rating ignored)", name)
		}
	}
	f.setPolicy(t, `{"MaxParentalRating":10}`)
	// Other read paths refuse the same items.
	for _, p := range []string{"/Items/%s/Ancestors", "/Items/%s/Similar", "/Items/%s/PlaybackInfo", "/UserItems/%s/UserData"} {
		path := strings.Replace(p, "%s", string(f.movie["R"]), 1)
		if code := f.get(t, f.aliceTok, path).status; code != 404 {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	if code := f.get(t, f.aliceTok, "/Shows/"+string(f.series)+"/Episodes").status; code != 404 {
		t.Errorf("episodes of a hidden series = %d, want 404", code)
	}
	if code := f.get(t, f.aliceTok, "/Shows/"+string(f.series)+"/Seasons").status; code != 404 {
		t.Errorf("seasons of a hidden series = %d, want 404", code)
	}
}

func TestRatingOtherListings(t *testing.T) {
	f := newRatingFixture(t)
	f.setPolicy(t, `{"MaxParentalRating":10}`)

	r := f.get(t, f.aliceTok, "/Items/Latest?IncludeItemTypes=Movie&Limit=50")
	if r.status != 200 {
		t.Fatalf("latest: %d", r.status)
	}
	// Latest answers a bare array, so decode it separately.
	req, _ := http.NewRequest(http.MethodGet, f.ts+"/Items/Latest?IncludeItemTypes=Movie&Limit=50", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+f.aliceTok+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var latest []baseItemDto
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		t.Fatal(err)
	}
	for _, it := range latest {
		if it.Name == "Rated R" || it.Name == "Rated NC-17" {
			t.Errorf("latest lists %q above the maximum", it.Name)
		}
	}
	if len(latest) == 0 {
		t.Error("latest is empty")
	}

	// Counts follow the caller's scope.
	req, _ = http.NewRequest(http.MethodGet, f.ts+"/Items/Counts", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+f.aliceTok+`"`)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var counts struct{ MovieCount int }
	_ = json.NewDecoder(resp2.Body).Decode(&counts)
	// alpha, bravo, charlie + G + PG + ABC (unrated text).
	if counts.MovieCount != 6 {
		t.Errorf("MovieCount = %d, want 6", counts.MovieCount)
	}

	// Resume hides an item that became too strict for the user.
	f.setPolicy(t, `{}`)
	body := `{"PlaybackPositionTicks":10000000}`
	if code, _ := policyDo(t, f.ts, f.aliceTok, http.MethodPost, "/UserItems/"+string(f.movie["R"])+"/UserData", body); code != 200 {
		t.Fatalf("set position: %d", code)
	}
	if r := f.get(t, f.aliceTok, "/UserItems/Resume"); len(r.Items) != 1 {
		t.Fatalf("resume before the restriction: %d", len(r.Items))
	}
	f.setPolicy(t, `{"MaxParentalRating":10}`)
	if r := f.get(t, f.aliceTok, "/UserItems/Resume"); len(r.Items) != 0 {
		t.Errorf("resume lists a hidden item: %d", len(r.Items))
	}
}
