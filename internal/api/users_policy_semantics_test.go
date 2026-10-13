package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// rawPolicy returns the Policy member of GET /Users/{id} as generic JSON.
func rawPolicy(t *testing.T, baseURL, token, userID string) map[string]any {
	t.Helper()
	code, body := policyDo(t, baseURL, token, http.MethodGet, "/Users/"+userID, "")
	if code != http.StatusOK {
		t.Fatalf("GET user: status %d", code)
	}
	var u struct{ Policy map[string]any }
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatal(err)
	}
	return u.Policy
}

// users.lockout.R1: LoginAttemptsBeforeLockout=0 is stored (and read back) as 3.
func TestPolicyLockoutZeroReadsBackAsThree(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy",
		`{`+providers+`,"LoginAttemptsBeforeLockout":0}`)
	if code != http.StatusNoContent {
		t.Fatalf("POST policy: %d", code)
	}
	if got := rawPolicy(t, ts.URL, tok, string(target.ID))["LoginAttemptsBeforeLockout"]; got != float64(3) {
		t.Errorf("LoginAttemptsBeforeLockout = %v, want 3", got)
	}
	for _, v := range []string{"-2", "5"} {
		policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy",
			`{`+providers+`,"LoginAttemptsBeforeLockout":`+v+`}`)
		if got := rawPolicy(t, ts.URL, tok, string(target.ID))["LoginAttemptsBeforeLockout"]; got != mustFloat(t, v) {
			t.Errorf("LoginAttemptsBeforeLockout %s read back as %v", v, got)
		}
	}
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	var f float64
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// users.lockout.R2-R3: the persisted failure counter counts every failure (also
// with no lockout configured) and the failure that triggers the lockout
// counts twice.
func TestFailureCounterObservable(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	count := func() float64 {
		return rawPolicy(t, ts.URL, tok, string(u.ID))["InvalidLoginAttemptCount"].(float64)
	}
	for i := 1; i <= 2; i++ {
		loginAs(t, ts.URL, "alice", "bad", "d")
		if got := count(); got != float64(i) {
			t.Fatalf("no lockout, failure %d: counter %v", i, got)
		}
	}
	if code, _ := loginAs(t, ts.URL, "alice", "pw", "d"); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if got := count(); got != 0 {
		t.Fatalf("counter after success = %v, want 0", got)
	}

	setPolicy(t, st, u.ID, func(p *userPolicyDto) { p.LoginAttemptsBeforeLockout = 3 })
	loginAs(t, ts.URL, "alice", "bad", "d")
	loginAs(t, ts.URL, "alice", "bad", "d")
	if got := count(); got != 2 {
		t.Fatalf("counter after two failures = %v, want 2", got)
	}
	loginAs(t, ts.URL, "alice", "bad", "d")
	p := rawPolicy(t, ts.URL, tok, string(u.ID))
	if p["IsDisabled"] != true || p["InvalidLoginAttemptCount"] != float64(4) {
		t.Errorf("after locking failure: IsDisabled=%v counter=%v, want true/4", p["IsDisabled"], p["InvalidLoginAttemptCount"])
	}
	// A disabled account is refused before the password is used: no count.
	loginAs(t, ts.URL, "alice", "bad", "d")
	if got := count(); got != 4 {
		t.Errorf("counter after refused login = %v, want 4", got)
	}
}

// users.schedule.R6-R7: DayOfWeek accepts names (any case), the numbers 0-9 and
// numeric strings, and always reads back canonical; unusable values answer 400.
func TestPolicyAccessScheduleDayForms(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	path := "/Users/" + string(target.ID) + "/Policy"
	day := func(t *testing.T) any {
		t.Helper()
		s := rawPolicy(t, ts.URL, tok, string(target.ID))["AccessSchedules"].([]any)
		if len(s) != 1 {
			t.Fatalf("schedules = %v", s)
		}
		return s[0].(map[string]any)["DayOfWeek"]
	}
	ok := []struct{ in, want any }{
		{`"tuesday"`, "Tuesday"}, {`"SUNDAY"`, "Sunday"}, {`2`, "Tuesday"}, {`"3"`, "Wednesday"},
		{`7`, "Everyday"}, {`8`, "Weekday"}, {`9`, "Weekend"}, {`"everyday"`, "Everyday"},
		{`10`, float64(10)}, {`-1`, float64(-1)},
	}
	for _, c := range ok {
		body := `{` + providers + `,"AccessSchedules":[{"DayOfWeek":` + c.in.(string) + `,"StartHour":1,"EndHour":2}]}`
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, path, body); code != http.StatusNoContent {
			t.Errorf("DayOfWeek %s: status %d, want 204", c.in, code)
			continue
		}
		if got := day(t); got != c.want {
			t.Errorf("DayOfWeek %s read back as %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{`"Foo"`, `""`, `null`, `3.5`} {
		body := `{` + providers + `,"AccessSchedules":[{"DayOfWeek":` + bad + `,"StartHour":1,"EndHour":2}]}`
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, path, body); code != http.StatusBadRequest {
			t.Errorf("DayOfWeek %s: status %d, want 400", bad, code)
		}
	}
	// Omitted members default to Sunday / 0; numeric strings are accepted
	// for the hours; null hours are refused.
	body := `{` + providers + `,"AccessSchedules":[{"StartHour":"3","EndHour":4.5}]}`
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, path, body); code != http.StatusNoContent {
		t.Fatalf("hours as string: %d", code)
	}
	s := rawPolicy(t, ts.URL, tok, string(target.ID))["AccessSchedules"].([]any)[0].(map[string]any)
	if s["DayOfWeek"] != "Sunday" || s["StartHour"] != float64(3) || s["EndHour"] != 4.5 {
		t.Errorf("schedule = %v", s)
	}
	body = `{` + providers + `,"AccessSchedules":[{"DayOfWeek":"Monday","StartHour":null,"EndHour":4}]}`
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, path, body); code != http.StatusBadRequest {
		t.Errorf("null StartHour: %d, want 400", code)
	}
}

// users.ids.R1-R2: a {userId} that is not a
// GUID, or the all-zero GUID, answers 400 on the policy and delete routes;
// every GUID spelling addresses the same account.
func TestUserPathIDsOnPolicyAndDelete(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	zero := "00000000000000000000000000000000"
	for _, id := range []string{"notaguid", "123", zero} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+id+"/Policy", `{`+providers+`}`); code != http.StatusBadRequest {
			t.Errorf("POST /Users/%s/Policy: %d, want 400", id, code)
		}
		if code, _ := policyDo(t, ts.URL, tok, http.MethodDelete, "/Users/"+id, ""); code != http.StatusBadRequest {
			t.Errorf("DELETE /Users/%s: %d, want 400", id, code)
		}
	}
	id := string(target.ID)
	dashed := id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+dashed+"/Policy", `{`+providers+`,"IsHidden":false}`); code != http.StatusNoContent {
		t.Errorf("POST policy by dashed id: %d, want 204", code)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodDelete, "/Users/"+dashed, ""); code != http.StatusNoContent {
		t.Errorf("DELETE by dashed id: %d, want 204", code)
	}
}

// users.shape.R1-R4: field-level shape of the user object, the preferences
// and the session returned by a login.
func TestUserObjectAndLoginSessionShape(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "alice", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	code, body := policyDo(t, ts.URL, tok, http.MethodGet, "/Users", "")
	if code != http.StatusOK {
		t.Fatalf("GET /Users: %d", code)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	var alice map[string]any
	for _, u := range list {
		if u["Name"] == "alice" {
			alice = u
		}
	}
	if alice == nil {
		t.Fatal("alice not listed")
	}
	if v, ok := alice["HasConfiguredEasyPassword"]; !ok || v != false {
		t.Errorf("HasConfiguredEasyPassword = %v (present %v), want false", v, ok)
	}
	if v := alice["EnableAutoLogin"]; v != false {
		t.Errorf("EnableAutoLogin = %v, want false", v)
	}
	if _, ok := alice["LastLoginDate"]; ok {
		t.Errorf("LastLoginDate present before the first login")
	}
	cfg := alice["Configuration"].(map[string]any)
	if v, ok := cfg["SubtitleLanguagePreference"]; !ok || v != "" {
		t.Errorf("SubtitleLanguagePreference = %v (present %v), want empty string", v, ok)
	}
	if _, ok := cfg["AudioLanguagePreference"]; ok {
		t.Errorf("AudioLanguagePreference present on a fresh account")
	}
	if cfg["CastReceiverId"] != "F007D354" {
		t.Errorf("CastReceiverId = %v, want F007D354", cfg["CastReceiverId"])
	}

	_, raw := loginReq(t, ts.URL, hdr("shape-dev"), `{"Username":"alice","Pw":"pw"}`)
	var res struct {
		SessionInfo map[string]any
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	si := res.SessionInfo
	for _, k := range []string{"PlayableMediaTypes", "SupportedCommands", "NowPlayingQueue", "AdditionalUsers"} {
		if a, ok := si[k].([]any); !ok || len(a) != 0 {
			t.Errorf("SessionInfo.%s = %v, want []", k, si[k])
		}
	}
	caps := si["Capabilities"].(map[string]any)
	for _, k := range []string{"PlayableMediaTypes", "SupportedCommands"} {
		if a, ok := caps[k].([]any); !ok || len(a) != 0 {
			t.Errorf("SessionInfo.Capabilities.%s = %v, want []", k, caps[k])
		}
	}
	if caps["SupportsPersistentIdentifier"] != true || caps["SupportsMediaControl"] != false {
		t.Errorf("Capabilities = %v", caps)
	}
	if si["IsActive"] != true || si["ApplicationVersion"] == "" {
		t.Errorf("IsActive/ApplicationVersion = %v/%v", si["IsActive"], si["ApplicationVersion"])
	}
}

// users.prefs.R1-R2: CastReceiverId defaults to the stable receiver and
// is only changed by a known receiver id; anything else leaves the stored
// value untouched, also when the body omits it.
func TestConfigurationCastReceiver(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	post := func(body string) {
		t.Helper()
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/Configuration?userId="+string(u.ID), body); code != http.StatusNoContent {
			t.Fatalf("POST configuration %s: %d", body, code)
		}
	}
	cast := func() any {
		t.Helper()
		_, b := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/"+string(u.ID), "")
		var d struct{ Configuration map[string]any }
		_ = json.Unmarshal(b, &d)
		return d.Configuration["CastReceiverId"]
	}
	steps := []struct{ body, want string }{
		{`{"CastReceiverId":"6F511C87"}`, "6F511C87"},
		{`{"CastReceiverId":"nope"}`, "6F511C87"},
		{`{}`, "6F511C87"},
		{`{"CastReceiverId":"6f511c87","SubtitleMode":"Always"}`, "6F511C87"},
		{`{"CastReceiverId":null}`, "6F511C87"},
		{`{"CastReceiverId":"F007D354"}`, "F007D354"},
	}
	for _, s := range steps {
		post(s.body)
		if got := cast(); got != s.want {
			t.Errorf("after %s: CastReceiverId = %v, want %s", s.body, got, s.want)
		}
	}
	// POST /Users without Configuration resets the preferences but keeps it.
	post(`{"CastReceiverId":"6F511C87","SubtitleMode":"Always"}`)
	upd := `{"Name":"alice",` + `"Policy":{` + providers + `}}`
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users?userId="+string(u.ID), upd); code != http.StatusNoContent {
		t.Fatalf("POST /Users: %d", code)
	}
	if got := cast(); got != "6F511C87" {
		t.Errorf("after POST /Users without Configuration: %v, want 6F511C87", got)
	}
}
