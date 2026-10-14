package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
)

var (
	hex32RE    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dashedRE   = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	upperHexRE = regexp.MustCompile(`[0-9A-F]{32}`)
)

// idKeyExempt names keys that end in "Id" but carry a value the client chose
// or a value that is not an identifier of a Tinyfin object.
var idKeyExempt = map[string]bool{
	"DeviceId": true, "ClientId": true, "DisplayPreferencesId": true,
	"AuthenticationProviderId": true, "PasswordResetProviderId": true, "CastReceiverId": true,
}

// walkWireIDs reports every identifier in the decoded JSON value v that
// breaks wire.id.R1: any string containing a dashed or upper-case GUID, and
// any non-empty string under an "...Id"/"...Ids" key that is not 32
// lowercase hex digits.
func walkWireIDs(t *testing.T, where string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "ImageTags" || k == "BackdropImageTags" {
				continue // values are image tags, not identifiers
			}
			switch s := val.(type) {
			case string:
				if (strings.HasSuffix(k, "Id") || strings.HasSuffix(k, "ID")) && !idKeyExempt[k] && s != "" && !hex32RE.MatchString(s) {
					if k != "Id" || dashedRE.MatchString(s) {
						t.Errorf("%s: %s = %q is not 32 lowercase hex", where, k, s)
					}
				}
			case []any:
				if strings.HasSuffix(k, "Ids") {
					for _, e := range s {
						if es, ok := e.(string); ok && es != "" && !hex32RE.MatchString(es) {
							t.Errorf("%s: %s contains %q, not 32 lowercase hex", where, k, es)
						}
					}
				}
			}
			walkWireIDs(t, where+"."+k, val)
		}
	case []any:
		for i, e := range x {
			walkWireIDs(t, where+"[]", e)
			_ = i
		}
	case string:
		if strings.Trim(x, "0") == "" {
			// the all-zero id (an unowned result's OwnerId) is neither dashed
			// nor upper case
			return
		}
		if dashedRE.MatchString(x) || upperHexRE.MatchString(x) {
			t.Errorf("%s: value %q embeds a dashed or upper-case id", where, x)
		}
	}
}

func wireDo(t *testing.T, base, token, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Client="WireTest", Device="WireTest", DeviceId="wire-test-device", Version="1.0", Token="`+token+`"`)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestWireIDsAreHex32 walks the JSON of the main responses and asserts every
// identifier is emitted as 32 lowercase hex digits (wire.id.R1).
func TestWireIDsAreHex32(t *testing.T) {
	f := newItemsFixture(t)
	alpha, show, season1, e11 := string(f.alpha), string(f.show), string(f.season1), string(f.e11)
	uid := string(f.admin.ID)

	// A playing session, so /Sessions and the playback paths carry ids.
	play := `{"ItemId":"` + alpha + `","MediaSourceId":"` + alpha + `","PlaySessionId":"` + guid.New() + `","PositionTicks":100}`
	if c, b := wireDo(t, f.ts, f.adminTok, "POST", "/Sessions/Playing", play); c != 204 {
		t.Fatalf("Sessions/Playing = %d %s", c, b)
	}

	gets := []string{
		"/System/Info", "/System/Info/Public", "/Users", "/Users/Me", "/Users/" + uid, "/Users/Public",
		"/UserViews", "/UserViews?userId=" + uid,
		"/Items?Recursive=true&Fields=ParentId,SeriesId,SeasonId,Path,MediaSources,People,Studios,Genres",
		"/Items?Recursive=true&Fields=ParentId&userId=" + uid,
		"/Items/" + alpha, "/Items/" + e11, "/Items/" + show, "/Items/" + season1,
		"/Users/" + uid + "/Items/" + alpha,
		"/Items/" + alpha + "/Ancestors", "/Items/" + alpha + "/Similar", "/Items/" + alpha + "/ThemeMedia",
		"/Items/" + alpha + "/PlaybackInfo", "/Items/" + e11 + "/PlaybackInfo",
		"/Items/Latest", "/Items/Filters", "/Items/Filters2", "/Items/Counts",
		"/UserItems/Resume", "/UserItems/" + alpha + "/UserData",
		"/Shows/" + show + "/Seasons", "/Shows/" + show + "/Episodes", "/Shows/NextUp", "/Studios",
		"/Library/VirtualFolders", "/Sessions", "/ScheduledTasks", "/System/ActivityLog/Entries",
		"/DisplayPreferences/usersettings?client=wire", "/QuickConnect/Enabled",
		"/MediaSegments/" + alpha, "/Devices",
	}
	for _, p := range gets {
		code, b := wireDo(t, f.ts, f.adminTok, "GET", p, "")
		if code == 404 && strings.HasPrefix(p, "/Devices") {
			continue
		}
		if code >= 500 {
			t.Errorf("GET %s = %d", p, code)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(string(b)), "{") && !strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
			continue
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			continue
		}
		walkWireIDs(t, "GET "+p, v)
	}

	// POST bodies that answer with ids.
	if c, b := wireDo(t, f.ts, f.adminTok, "POST", "/Items/"+alpha+"/PlaybackInfo", `{"UserId":"`+uid+`"}`); c == 200 {
		var v any
		_ = json.Unmarshal(b, &v)
		walkWireIDs(t, "POST PlaybackInfo", v)
	}
	resp := authenticate(t, f.ts, "alice", "alicepass")
	var auth any
	_ = json.NewDecoder(resp.Body).Decode(&auth)
	resp.Body.Close()
	walkWireIDs(t, "POST /Users/AuthenticateByName", auth)
	if c, b := wireDo(t, f.ts, "", "POST", "/QuickConnect/Initiate", ""); c == 200 {
		var v any
		_ = json.Unmarshal(b, &v)
		walkWireIDs(t, "POST /QuickConnect/Initiate", v)
	}

	// Stream/image/HLS URLs inside PlaybackInfo are covered by the walk's
	// embedded-id check; make sure at least one such URL was produced.
	_, b := wireDo(t, f.ts, f.adminTok, "GET", "/Items/"+alpha+"/PlaybackInfo", "")
	if !bytes.Contains(b, []byte(alpha)) {
		t.Errorf("PlaybackInfo does not mention the item id: %s", b)
	}
}

// TestWireDashedInputAccepted is the regression test for wire.id.R2: ids in
// the dashed form (and in upper case) are accepted in a path, a query
// parameter and a request body, and name the same object as the canonical
// form.
func TestWireDashedInputAccepted(t *testing.T) {
	f := newItemsFixture(t)
	alpha := string(f.alpha)
	dashed := guid.Dashed(alpha)
	upper := strings.ToUpper(alpha)
	uid := string(f.admin.ID)

	type idOf struct {
		Id     string
		UserId string
	}
	idFrom := func(path string) string {
		t.Helper()
		code, b := wireDo(t, f.ts, f.adminTok, "GET", path, "")
		if code != 200 {
			t.Fatalf("GET %s = %d %s", path, code, b)
		}
		var o idOf
		_ = json.Unmarshal(b, &o)
		return o.Id
	}

	// Path.
	for _, spelling := range []string{dashed, upper} {
		if got := idFrom("/Items/" + spelling); got != alpha {
			t.Errorf("path %s: Id = %q, want %q", spelling, got, alpha)
		}
		if got := idFrom("/Users/" + guid.Dashed(uid)); got != uid {
			t.Errorf("user path: Id = %q, want %q", got, uid)
		}
	}
	if c, _ := wireDo(t, f.ts, f.adminTok, "GET", "/Users/"+guid.Dashed(uid)+"/Items/"+dashed, ""); c != 200 {
		t.Errorf("legacy user items path with dashed ids = %d", c)
	}

	// Handlers that take the id straight from the path must see the same
	// object under every spelling.
	for _, p := range []string{
		"/Shows/" + guid.Dashed(string(f.show)) + "/Seasons",
		"/Shows/" + strings.ToUpper(string(f.show)) + "/Episodes",
		"/Items/" + dashed + "/PlaybackInfo",
		"/Items/" + dashed + "/ThemeMedia",
		"/Items/" + dashed + "/Ancestors",
		"/UserItems/" + dashed + "/UserData",
		"/Items/" + dashed + "/Images",
		"/MediaSegments/" + dashed,
	} {
		if c, b := wireDo(t, f.ts, f.adminTok, "GET", p, ""); c != 200 {
			t.Errorf("GET %s = %d %s", p, c, b)
		}
	}
	for _, p := range []string{"/UserFavoriteItems/" + dashed, "/UserPlayedItems/" + upper} {
		if c, b := wireDo(t, f.ts, f.adminTok, "POST", p, ""); c != 200 {
			t.Errorf("POST %s = %d %s", p, c, b)
		}
	}
	if _, b := wireDo(t, f.ts, f.adminTok, "GET", "/Items/"+alpha, ""); !strings.Contains(string(b), `"IsFavorite":true`) {
		t.Errorf("favorite through the dashed path did not land on the canonical item")
	}

	// Query: scalar and comma-separated, and a ParentId.
	listIDs := func(q string) []string {
		t.Helper()
		code, b := wireDo(t, f.ts, f.adminTok, "GET", "/Items?"+q, "")
		if code != 200 {
			t.Fatalf("GET /Items?%s = %d %s", q, code, b)
		}
		var r struct{ Items []struct{ Id string } }
		_ = json.Unmarshal(b, &r)
		var out []string
		for _, i := range r.Items {
			out = append(out, i.Id)
		}
		return out
	}
	q := url.Values{"Ids": {dashed + "," + upper}, "UserId": {guid.Dashed(uid)}}.Encode()
	if got := listIDs(q); len(got) != 1 || got[0] != alpha {
		t.Errorf("Ids query = %v, want [%s]", got, alpha)
	}
	pq := url.Values{"ParentId": {guid.Dashed(string(f.show))}}.Encode()
	if got := listIDs(pq); len(got) != 2 {
		t.Errorf("ParentId dashed = %v, want the two seasons", got)
	}

	// Body: a playback report keyed by dashed ids, then read back through
	// the canonical user-data path.
	body := `{"ItemId":"` + dashed + `","MediaSourceId":"` + dashed + `","PositionTicks":100}`
	if c, b := wireDo(t, f.ts, f.adminTok, "POST", "/Sessions/Playing", body); c != 204 {
		t.Fatalf("Sessions/Playing dashed body = %d %s", c, b)
	}
	// The echoed MediaSourceId is canonical, and the omitted one defaults
	// to the item id (playstate.session.R8).
	_, sb := wireDo(t, f.ts, f.adminTok, "GET", "/Sessions", "")
	var sess []struct {
		PlayState struct{ MediaSourceId string }
	}
	_ = json.Unmarshal(sb, &sess)
	found := false
	for _, x := range sess {
		if x.PlayState.MediaSourceId == alpha {
			found = true
		}
	}
	if !found {
		t.Errorf("no session carried MediaSourceId %s (sessions: %s)", alpha, sb)
	}
	if c, b := wireDo(t, f.ts, f.adminTok, "POST", "/Sessions/Playing/Stopped", `{"ItemId":"`+dashed+`","PositionTicks":100}`); c != 204 {
		t.Fatalf("Sessions/Playing/Stopped dashed body = %d %s", c, b)
	}
	// Policy body: dashed folder ids come back canonical.
	pol := `{"EnableAllFolders":false,"EnabledFolders":["` + guid.Dashed(string(f.movies)) + `"],"AuthenticationProviderId":"Tinyfin.Auth.DefaultAuthenticationProvider","PasswordResetProviderId":"Tinyfin.Auth.DefaultPasswordResetProvider"}`
	if c, b := wireDo(t, f.ts, f.adminTok, "POST", "/Users/"+string(f.alice.ID)+"/Policy", pol); c != 204 {
		t.Fatalf("Policy = %d %s", c, b)
	}
	_, b := wireDo(t, f.ts, f.adminTok, "GET", "/Users/"+string(f.alice.ID), "")
	var u struct {
		Policy struct{ EnabledFolders []string }
	}
	_ = json.Unmarshal(b, &u)
	if len(u.Policy.EnabledFolders) != 1 || u.Policy.EnabledFolders[0] != string(f.movies) {
		t.Errorf("EnabledFolders = %v, want [%s]", u.Policy.EnabledFolders, f.movies)
	}
}
