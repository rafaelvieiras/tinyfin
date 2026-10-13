package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// wire.path.R1-R4: one trailing slash is ignored on every route and method.
func TestTrailingSlash(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	for _, p := range []string{
		"/Users", "/Users/Public", "/Users/Me", "/Users/" + string(admin.ID),
		"/System/Info/Public", "/System/Info", "/Items", "/Sessions",
		"/System/ActivityLog/Entries", "/QuickConnect/Enabled",
		"/users", "/SYSTEM/info/public", "/Users?isHidden=false",
	} {
		base, query, _ := strings.Cut(p, "?")
		slashed := base + "/"
		if query != "" {
			slashed += "?" + query
		}
		wantCode, wantBody := policyDo(t, ts.URL, tok, http.MethodGet, p, "")
		code, body := policyDo(t, ts.URL, tok, http.MethodGet, slashed, "")
		if code != wantCode {
			t.Errorf("GET %s = %d, want %d (same as without the slash)", slashed, code, wantCode)
		}
		if wantCode == http.StatusOK && len(body) == 0 && len(wantBody) != 0 {
			t.Errorf("GET %s: empty body", slashed)
		}
	}

	// Authentication still applies.
	if code, _ := policyDo(t, ts.URL, "", http.MethodGet, "/Users/", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /Users/ = %d, want 401", code)
	}

	// Other methods.
	code, body := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New/", `{"Name":"slashed"}`)
	if code != http.StatusOK {
		t.Fatalf("POST /Users/New/ = %d (%s), want 200", code, body)
	}
	var created struct{ Id string }
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration/?userId="+created.Id, `{}`); code != http.StatusNoContent {
		t.Errorf("POST /Users/Configuration/ = %d, want 204", code)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodDelete, "/Users/"+created.Id+"/", ""); code != http.StatusNoContent {
		t.Errorf("DELETE /Users/{id}/ = %d, want 204", code)
	}
	code, _ = loginReq(t, ts.URL, hdr("slash-dev"), `{"Username":"admin","Pw":"adminpass"}`)
	if code != http.StatusOK {
		t.Errorf("login with the plain path = %d", code)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/Users/AuthenticateByName/", strings.NewReader(`{"Username":"admin","Pw":"adminpass"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", hdr("slash-dev2"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /Users/AuthenticateByName/ = %d, want 200", resp.StatusCode)
	}

	// Only one slash is dropped; empty segments and unknown routes stay 404.
	for _, p := range []string{"/Users//", "/Users/Me//", "/nonexistent/", "/Users//Me"} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodGet, p, ""); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
}

// wire.date.R1-R4.
func TestFormatWireTime(t *testing.T) {
	base := time.Date(2026, 10, 13, 16, 2, 24, 0, time.UTC)
	cases := []struct {
		t    time.Time
		want string
	}{
		{base, "2026-10-13T16:02:24.0000000Z"},
		{base.Add(329574700 * time.Nanosecond), "2026-10-13T16:02:24.3295747Z"},
		{base.Add(329574000 * time.Nanosecond), "2026-10-13T16:02:24.329574Z"},
		{base.Add(500 * time.Millisecond), "2026-10-13T16:02:24.5Z"},
		{base.Add(123456789 * time.Nanosecond), "2026-10-13T16:02:24.1234567Z"},
		{base.Add(999999999 * time.Nanosecond), "2026-10-13T16:02:24.9999999Z"},
		{base.Add(99 * time.Nanosecond), "2026-10-13T16:02:24.0000000Z"},
		{base.Add(100 * time.Nanosecond), "2026-10-13T16:02:24.0000001Z"},
		{time.Time{}, "0001-01-01T00:00:00.0000000Z"},
		{time.Date(2026, 10, 13, 18, 2, 24, 0, time.FixedZone("x", 2*3600)), "2026-10-13T16:02:24.0000000Z"},
	}
	for _, c := range cases {
		if got := formatWireTime(c.t); got != c.want {
			t.Errorf("formatWireTime(%v) = %q, want %q", c.t, got, c.want)
		}
	}
	raw, err := json.Marshal(struct {
		A wireTime  `json:"A"`
		B *wireTime `json:"B,omitempty"`
		C *wireTime `json:"C,omitempty"`
	}{A: wireTime{base.Add(500 * time.Millisecond)}, B: &wireTime{base}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"A":"2026-10-13T16:02:24.5Z","B":"2026-10-13T16:02:24.0000000Z"}`; string(raw) != want {
		t.Errorf("json = %s, want %s", raw, want)
	}
	var back struct{ A wireTime }
	if err := json.Unmarshal([]byte(`{"A":"2026-10-13T16:02:24.5Z"}`), &back); err != nil || !back.A.Equal(base.Add(500*time.Millisecond)) {
		t.Errorf("round trip: %v %v", back.A, err)
	}
}

var wireDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{0,6}[1-9]|\.0000000)Z$`)

func TestDatesOnTheWire(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	for i := 0; i < 30; i++ {
		_, raw := loginReq(t, ts.URL, hdr("date-dev"), `{"Username":"admin","Pw":"adminpass"}`)
		var res struct {
			User        map[string]any
			SessionInfo map[string]any
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		for name, v := range map[string]any{
			"User.LastLoginDate":            res.User["LastLoginDate"],
			"User.LastActivityDate":         res.User["LastActivityDate"],
			"SessionInfo.LastActivityDate":  res.SessionInfo["LastActivityDate"],
			"SessionInfo.LastPlaybackCheck": res.SessionInfo["LastPlaybackCheckIn"],
		} {
			s, _ := v.(string)
			if !wireDateRE.MatchString(s) {
				t.Fatalf("%s = %q does not match the wire date format", name, s)
			}
		}
		if got := res.SessionInfo["LastPlaybackCheckIn"]; got != "0001-01-01T00:00:00.0000000Z" {
			t.Fatalf("LastPlaybackCheckIn = %v", got)
		}
	}
}

// users.shape.R5: the user object has no ServerName member.
func TestUserObjectHasNoServerName(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	for _, p := range []string{"/Users/Me", "/Users", "/Users/Public"} {
		_, body := policyDo(t, ts.URL, tok, http.MethodGet, p, "")
		if strings.Contains(string(body), `"ServerName"`) {
			t.Errorf("GET %s carries ServerName: %s", p, body)
		}
	}
	_, raw := loginReq(t, ts.URL, hdr("sn-dev"), `{"Username":"admin","Pw":"adminpass"}`)
	if strings.Contains(string(raw), `"ServerName"`) {
		t.Errorf("login result carries ServerName: %s", raw)
	}
	// users.shape.R6: no NowPlayingQueueFullItems member on the session.
	if strings.Contains(string(raw), "NowPlayingQueueFullItems") {
		t.Errorf("login result carries NowPlayingQueueFullItems: %s", raw)
	}
}

// users.configuration.R6: an empty AudioLanguagePreference is kept; null
// and omission leave it absent.
func TestAudioLanguagePreferencePresence(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	audio := func(path string) (any, bool) {
		_, body := policyDo(t, ts.URL, tok, http.MethodGet, path, "")
		var u map[string]any
		var list []map[string]any
		if strings.HasPrefix(string(body), "[") {
			if err := json.Unmarshal(body, &list); err != nil {
				t.Fatal(err)
			}
			u = list[0]
		} else if err := json.Unmarshal(body, &u); err != nil {
			t.Fatal(err)
		}
		v, ok := u["Configuration"].(map[string]any)["AudioLanguagePreference"]
		return v, ok
	}
	post := func(body string) {
		t.Helper()
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration", body); code != http.StatusNoContent {
			t.Fatalf("POST /Users/Configuration %s = %d", body, code)
		}
	}
	if _, ok := audio("/Users/Me"); ok {
		t.Error("present on a fresh account")
	}
	post(`{"AudioLanguagePreference":""}`)
	for _, p := range []string{"/Users/Me", "/Users"} {
		if v, ok := audio(p); !ok || v != "" {
			t.Errorf("%s after \"\": %v present=%v", p, v, ok)
		}
	}
	post(`{"AudioLanguagePreference":"eng"}`)
	if v, _ := audio("/Users/Me"); v != "eng" {
		t.Errorf("after eng: %v", v)
	}
	post(`{"AudioLanguagePreference":null}`)
	if _, ok := audio("/Users/Me"); ok {
		t.Error("present after null")
	}
	post(`{"AudioLanguagePreference":""}`)
	post(`{}`)
	if _, ok := audio("/Users/Me"); ok {
		t.Error("present after a body that omits it")
	}
	// Whole-object update keeps the empty string too.
	_, me := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/Me", "")
	var u map[string]any
	_ = json.Unmarshal(me, &u)
	u["Configuration"].(map[string]any)["AudioLanguagePreference"] = ""
	b, _ := json.Marshal(u)
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users", string(b)); code != http.StatusNoContent {
		t.Fatalf("POST /Users = %d", code)
	}
	if v, ok := audio("/Users/Me"); !ok || v != "" {
		t.Errorf("after POST /Users: %v present=%v", v, ok)
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// wire.date.R2/R4: the date-time forms a client may send in user data.
func TestUserDataLastPlayedDateForms(t *testing.T) {
	e := newUDEnv(t)
	cases := []struct{ in, want string }{
		{"2024-05-06T07:08:09Z", "2024-05-06T07:08:09.0000000Z"},
		{"2024-05-06T07:08:09.1000000Z", "2024-05-06T07:08:09.1Z"},
		{"2024-05-06T07:08:09.123456789Z", "2024-05-06T07:08:09.1234567Z"},
		{"2024-05-06T07:08:09+02:00", "2024-05-06T05:08:09.0000000Z"},
		{"2024-05-06T07:08:09", "2024-05-06T07:08:09.0000000Z"},
		{"2024-05-06T07:08:09.25", "2024-05-06T07:08:09.25Z"},
	}
	for _, c := range cases {
		code, got := udDo(t, e.base, e.aliceTok, "POST", e.path(e.movies[0], ""), map[string]any{"LastPlayedDate": c.in})
		if code != http.StatusOK {
			t.Errorf("LastPlayedDate %q: status %d", c.in, code)
			continue
		}
		if got["LastPlayedDate"] != c.want {
			t.Errorf("LastPlayedDate %q = %v, want %s", c.in, got["LastPlayedDate"], c.want)
		}
	}
}
