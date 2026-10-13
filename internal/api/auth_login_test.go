package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// loginReq posts to /Users/AuthenticateByName with a raw Authorization
// header (empty means "send none") and a raw JSON body.
func loginReq(t *testing.T, baseURL, authz, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/Users/AuthenticateByName", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func hdr(deviceID string) string {
	return `MediaBrowser Client="C", Device="D", DeviceId="` + deviceID + `", Version="1"`
}

func loginAs(t *testing.T, baseURL, name, pw, deviceID string) (int, authenticationResult) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"Username": name, "Pw": pw})
	code, raw := loginReq(t, baseURL, hdr(deviceID), string(b))
	var res authenticationResult
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("decoding result: %v", err)
		}
	}
	return code, res
}

// setPolicy rewrites a stored user's policy through the storage layer.
func setPolicy(t *testing.T, st storage.UserStore, id storage.UserID, mut func(*userPolicyDto)) {
	t.Helper()
	u, err := st.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	p := storedUserPolicy(u)
	mut(&p)
	p.normalize()
	raw, _ := json.Marshal(p)
	u.Policy = string(raw)
	u.IsAdministrator = p.IsAdministrator
	u.IsDisabled = p.IsDisabled
	if err := st.UpdateUser(context.Background(), u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
}

func meStatus(t *testing.T, baseURL, token, deviceID string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/Users/Me", nil)
	req.Header.Set("Authorization", hdr(deviceID)+`, Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /Users/Me: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// users.auth.R6, R11: header presence and parsing.
func TestAuthenticateHeaderRules(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	body := `{"Username":"alice","Pw":"pw"}`
	wrong := `{"Username":"alice","Pw":"nope"}`

	cases := []struct {
		name, authz, body string
		want              int
	}{
		{"absent header", "", body, 400},
		{"absent header wrong password", "", wrong, 400},
		{"missing client", `MediaBrowser Device="D", DeviceId="x", Version="1"`, body, 400},
		{"missing device", `MediaBrowser Client="C", DeviceId="x", Version="1"`, body, 400},
		{"missing device id", `MediaBrowser Client="C", Device="D", Version="1"`, body, 400},
		{"missing version", `MediaBrowser Client="C", Device="D", DeviceId="x"`, body, 400},
		{"lower-case key", `MediaBrowser Client="C", Device="D", deviceid="x", Version="1"`, body, 400},
		{"emby scheme", `Emby Client="C", Device="D", DeviceId="x", Version="1"`, body, 400},
		{"scheme case-insensitive", `mediabrowser Client="C", Device="D", DeviceId="x", Version="1"`, body, 200},
		{"comma in quoted value", `MediaBrowser Client="C", Device="Phone, Kitchen", DeviceId="y", Version="1"`, body, 200},
		{"missing username", hdr("z"), `{"Pw":"pw"}`, 400},
		{"blank username", hdr("z"), `{"Username":"   ","Pw":"pw"}`, 400},
		{"malformed body", hdr("z"), `{`, 400},
	}
	for _, c := range cases {
		if code, _ := loginReq(t, ts.URL, c.authz, c.body); code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, code, c.want)
		}
	}
}

func TestAuthenticateSessionInfoAndDecodedHeader(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	authz := `MediaBrowser Client="C", Device="My%20Phone", DeviceId="a", DeviceId="b", Version="1"`
	code, raw := loginReq(t, ts.URL, authz, `{"Username":"alice","Pw":"pw"}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var res struct {
		SessionInfo struct{ UserId, DeviceId, Client, DeviceName string }
		AccessToken string
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.SessionInfo.DeviceId != "b" || res.SessionInfo.DeviceName != "My Phone" || res.SessionInfo.Client != "C" || res.SessionInfo.UserId == "" {
		t.Errorf("SessionInfo = %+v", res.SessionInfo)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(res.AccessToken) {
		t.Errorf("AccessToken %q is not 32 lower-case hex digits", res.AccessToken)
	}
}

func TestAuthenticateUsernameCaseInsensitive(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "Alice", "pw")
	if code, _ := loginAs(t, ts.URL, "aLiCe", "pw", "d1"); code != 200 {
		t.Errorf("case-insensitive login: %d, want 200", code)
	}
}

// users.auth.R10.
func TestAuthenticateReloginSameDeviceRevokes(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestUser(t, st, "bob", "pw")
	_, a1 := loginAs(t, ts.URL, "alice", "pw", "dev")
	_, other := loginAs(t, ts.URL, "alice", "pw", "dev-other")
	_, bob := loginAs(t, ts.URL, "bob", "pw", "dev")
	_, a2 := loginAs(t, ts.URL, "alice", "pw", "dev")
	if a1.AccessToken == a2.AccessToken {
		t.Fatal("tokens are equal")
	}
	if c := meStatus(t, ts.URL, a1.AccessToken, "dev"); c != 401 {
		t.Errorf("revoked token: %d, want 401", c)
	}
	for name, tok := range map[string]string{"new": a2.AccessToken, "other device": other.AccessToken, "other user": bob.AccessToken} {
		if c := meStatus(t, ts.URL, tok, "dev"); c != 200 {
			t.Errorf("%s token: %d, want 200", name, c)
		}
	}
}

// users.auth.R13-R15.
func TestAuthenticateLockout(t *testing.T) {
	ts, st := newTestServer(t)
	u := createTestUser(t, st, "alice", "pw")

	// Default: no lockout.
	for i := 0; i < 6; i++ {
		if code, _ := loginAs(t, ts.URL, "alice", "bad", "d"); code != 401 {
			t.Fatalf("default attempt %d: %d, want 401", i, code)
		}
	}
	if code, _ := loginAs(t, ts.URL, "alice", "pw", "d"); code != 200 {
		t.Fatalf("default correct password: %d", code)
	}

	// Limit 2, counter reset by a success.
	setPolicy(t, st, u.ID, func(p *userPolicyDto) { p.LoginAttemptsBeforeLockout = 2 })
	loginAs(t, ts.URL, "alice", "bad", "d")
	if code, _ := loginAs(t, ts.URL, "alice", "pw", "d"); code != 200 {
		t.Fatalf("after one failure: %d", code)
	}
	loginAs(t, ts.URL, "alice", "bad", "d")
	if code, res := loginAs(t, ts.URL, "alice", "pw", "d2"); code != 200 {
		t.Fatalf("failures separated by a success must not lock: %d", code)
	} else {
		loginAs(t, ts.URL, "alice", "bad", "d")
		if code, _ := loginAs(t, ts.URL, "alice", "bad", "d"); code != 401 {
			t.Fatalf("locking attempt answers %d, want 401", code)
		}
		if code, _ := loginAs(t, ts.URL, "alice", "pw", "d"); code != 403 {
			t.Errorf("after lockout: %d, want 403", code)
		}
		if c := meStatus(t, ts.URL, res.AccessToken, "d2"); c != 401 {
			t.Errorf("existing token after lockout: %d, want 401", c)
		}
	}

	// Zero behaves as three.
	v := createTestUser(t, st, "bob", "pw")
	setPolicy(t, st, v.ID, func(p *userPolicyDto) { p.LoginAttemptsBeforeLockout = 0 })
	loginAs(t, ts.URL, "bob", "bad", "e")
	loginAs(t, ts.URL, "bob", "bad", "e")
	if code, _ := loginAs(t, ts.URL, "bob", "pw", "e"); code != 200 {
		t.Fatalf("two failures with limit 0 locked: %d", code)
	}
	for i := 0; i < 3; i++ {
		loginAs(t, ts.URL, "bob", "bad", "e")
	}
	if code, _ := loginAs(t, ts.URL, "bob", "pw", "e"); code != 403 {
		t.Errorf("three failures with limit 0: %d, want 403", code)
	}

	// Unknown users touch no counter and are indistinguishable (401).
	if code, _ := loginAs(t, ts.URL, "ghost", "x", "e"); code != 401 {
		t.Errorf("unknown user: %d, want 401", code)
	}
}

// users.auth.R16-R18.
func TestAuthenticateRestrictions(t *testing.T) {
	ts, st := newTestServer(t)

	m := createTestUser(t, st, "maxed", "pw")
	setPolicy(t, st, m.ID, func(p *userPolicyDto) { p.MaxActiveSessions = 1 })
	if code, _ := loginAs(t, ts.URL, "maxed", "pw", "a"); code != 200 {
		t.Fatalf("first session: %d", code)
	}
	if code, _ := loginAs(t, ts.URL, "maxed", "pw", "b"); code != 403 {
		t.Errorf("second device: %d, want 403", code)
	}
	if code, _ := loginAs(t, ts.URL, "maxed", "pw", "a"); code != 200 {
		t.Errorf("same device again: %d, want 200", code)
	}
	setPolicy(t, st, m.ID, func(p *userPolicyDto) { p.MaxActiveSessions = 0 })
	if code, _ := loginAs(t, ts.URL, "maxed", "pw", "b"); code != 200 {
		t.Errorf("unlimited: %d, want 200", code)
	}

	d := createTestUser(t, st, "devs", "pw")
	setPolicy(t, st, d.ID, func(p *userPolicyDto) { p.EnableAllDevices = false; p.EnabledDevices = []string{"ok"} })
	if code, _ := loginAs(t, ts.URL, "devs", "pw", "nope"); code != 403 {
		t.Errorf("device not allowed: %d, want 403", code)
	}
	if code, _ := loginAs(t, ts.URL, "devs", "pw", "ok"); code != 200 {
		t.Errorf("allowed device: %d, want 200", code)
	}

	// An schedule that excludes the current moment, for an administrator too.
	s := createTestAdminUser(t, st, "sched", "pw")
	now := time.Now()
	other := now.Add(-3 * time.Hour)
	setPolicy(t, st, s.ID, func(p *userPolicyDto) {
		p.AccessSchedules = []accessScheduleDto{{DayOfWeek: other.Weekday().String(), StartHour: 0, EndHour: 0}}
		// Same weekday as now but the window is at midnight only.
		p.AccessSchedules[0].DayOfWeek = now.Weekday().String()
		if now.Hour() == 0 {
			p.AccessSchedules[0].StartHour, p.AccessSchedules[0].EndHour = 12, 12
		}
	})
	if code, _ := loginAs(t, ts.URL, "sched", "pw", "s"); code != 403 {
		t.Errorf("outside schedule: %d, want 403", code)
	}
	setPolicy(t, st, s.ID, func(p *userPolicyDto) {
		p.AccessSchedules = []accessScheduleDto{{DayOfWeek: "Everyday", StartHour: 0, EndHour: 24}}
	})
	if code, _ := loginAs(t, ts.URL, "sched", "pw", "s"); code != 200 {
		t.Errorf("inside schedule: %d, want 200", code)
	}
}

// users.me.R5, users.get.R7: non-administrators outside a window.
func TestAccessScheduleOnAuthenticatedRequests(t *testing.T) {
	ts, st := newTestServer(t)
	u := createTestUser(t, st, "alice", "pw")
	_, res := loginAs(t, ts.URL, "alice", "pw", "d")
	now := time.Now()
	setPolicy(t, st, u.ID, func(p *userPolicyDto) {
		p.AccessSchedules = []accessScheduleDto{{DayOfWeek: now.Weekday().String(), StartHour: 0, EndHour: 0}}
		if now.Hour() == 0 {
			p.AccessSchedules[0].StartHour, p.AccessSchedules[0].EndHour = 12, 12
		}
	})
	if c := meStatus(t, ts.URL, res.AccessToken, "d"); c != 403 {
		t.Errorf("/Users/Me outside schedule: %d, want 403", c)
	}
	if c, _ := policyDo(t, ts.URL, res.AccessToken, http.MethodGet, "/Users", ""); c != 403 {
		t.Errorf("/Users outside schedule: %d, want 403", c)
	}
	if c, _ := policyDo(t, ts.URL, res.AccessToken, http.MethodGet, "/Users/"+string(u.ID), ""); c != 200 {
		t.Errorf("own profile outside schedule: %d, want 200", c)
	}
}

// users.token.R4: tokens of a disabled account are rejected even when the
// account was disabled without going through the policy endpoint.
func TestDisabledAccountTokenRejected(t *testing.T) {
	ts, st := newTestServer(t)
	u := createTestUser(t, st, "alice", "pw")
	_, res := loginAs(t, ts.URL, "alice", "pw", "d")
	u.IsDisabled = true
	if err := st.UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if c := meStatus(t, ts.URL, res.AccessToken, "d"); c != 401 {
		t.Errorf("token of disabled account: %d, want 401", c)
	}
}

// users.auth.R22.
func TestAuthenticateRecordsLastLogin(t *testing.T) {
	ts, st := newTestServer(t)
	u := createTestUser(t, st, "alice", "pw")
	loginAs(t, ts.URL, "alice", "pw", "d")
	got, _ := st.GetUser(context.Background(), u.ID)
	if got.LastLoginDate == nil || time.Since(*got.LastLoginDate) > time.Minute {
		t.Errorf("LastLoginDate = %v", got.LastLoginDate)
	}
}

// playstate.auth.R1.
func TestPlaystateRoutesRequireAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/UserPlayedItems/abc"},
		{"DELETE", "/UserPlayedItems/abc"},
		{"POST", "/Sessions/Playing"},
		{"POST", "/Sessions/Playing/Progress"},
		{"POST", "/Sessions/Playing/Ping?playSessionId=x"},
		{"POST", "/Sessions/Playing/Stopped"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("anonymous %s %s: %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}
