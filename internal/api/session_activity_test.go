package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Tests for the account/session activity entries (users.auth.R28-R32,
// users.token.R7-R9, users.create.R10-R11, users.delete.R8,
// users.password.R15-R16, users.policy.R14).

func sessReq(t *testing.T, base, method, path, device, deviceID, token string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	h := fmt.Sprintf(`MediaBrowser Client="T", Device="%s", DeviceId="%s", Version="1"`, device, deviceID)
	if token != "" {
		h += fmt.Sprintf(`, Token="%s"`, token)
	}
	req.Header.Set("Authorization", h)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func sessLogin(t *testing.T, base, user, pw, device, deviceID string) (int, string) {
	t.Helper()
	code, out := sessReq(t, base, "POST", "/Users/AuthenticateByName", device, deviceID, "", map[string]string{"Username": user, "Pw": pw})
	var res struct{ AccessToken string }
	_ = json.Unmarshal(out, &res)
	return code, res.AccessToken
}

// recentActivity returns the newest n entries as "Type|Name|ShortOverview",
// newest first.
func recentActivity(t *testing.T, st storage.Store, n int) []string {
	t.Helper()
	entries, _, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: n})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%s|%s|%s", e.Type, e.Name, e.ShortOverview))
	}
	return out
}

func wantActivity(t *testing.T, st storage.Store, want ...string) {
	t.Helper()
	got := recentActivity(t, st, len(want))
	if len(got) != len(want) {
		t.Fatalf("activity entries = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activity[%d] = %q, want %q\nall: %q", i, got[i], want[i], got)
		}
	}
}

func sessSetPolicy(t *testing.T, base, adminTok, userID string, st storage.Store, mutate func(p map[string]any)) {
	t.Helper()
	u, err := st.GetUser(context.Background(), storage.UserID(userID))
	if err != nil {
		t.Fatal(err)
	}
	pol := map[string]any{}
	if u.Policy != "" {
		if err := json.Unmarshal([]byte(u.Policy), &pol); err != nil {
			t.Fatal(err)
		}
	}
	pol["AuthenticationProviderId"] = "p"
	pol["PasswordResetProviderId"] = "p"
	mutate(pol)
	code, out := sessReq(t, base, "POST", "/Users/"+userID+"/Policy", "Adm", "adm-dev", adminTok, pol)
	if code != http.StatusNoContent {
		t.Fatalf("policy status = %d: %s", code, out)
	}
}

func TestLockoutWritesUserLockedOutBetweenFailures(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, func(p map[string]any) { p["LoginAttemptsBeforeLockout"] = 2 })

	sessLogin(t, ts.URL, "alice", "bad1", "Phone", "d1")
	sessLogin(t, ts.URL, "alice", "bad2", "Phone", "d1")
	got := recentActivity(t, st, 3)
	want := []string{"AuthenticationFailed|Failed login attempt from alice|", "UserLockedOut|User alice has been locked out|", "AuthenticationFailed|Failed login attempt from alice|"}
	for i, w := range want {
		// ShortOverview of the failures is an address; compare the prefix.
		if len(got[i]) < len(w) || got[i][:len(w)] != w {
			t.Fatalf("activity[%d] = %q, want prefix %q", i, got[i], w)
		}
	}
	entries, _, _ := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 2})
	if entries[1].Severity != storage.ActivitySeverityError || entries[1].UserID == nil || *entries[1].UserID != u.ID || entries[1].ShortOverview != "" {
		t.Fatalf("lockout entry = %+v", entries[1])
	}

	// A refused login on the locked account writes nothing.
	before := len(recentActivity(t, st, 100))
	if code, _ := sessLogin(t, ts.URL, "alice", "pw", "Phone", "d1"); code != http.StatusForbidden {
		t.Fatalf("locked login status = %d", code)
	}
	if after := len(recentActivity(t, st, 100)); after != before {
		t.Fatalf("refusal wrote %d entries", after-before)
	}
}

func TestLockoutKeepsTokens(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, func(p map[string]any) { p["LoginAttemptsBeforeLockout"] = 1 })
	_, tok := sessLogin(t, ts.URL, "alice", "pw", "Phone", "d1")
	sessLogin(t, ts.URL, "alice", "bad", "Phone", "d1")

	if code, _ := sessReq(t, ts.URL, "GET", "/Users/Me", "Phone", "d1", tok, nil); code != http.StatusUnauthorized {
		t.Fatalf("token while disabled = %d, want 401", code)
	}
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, func(p map[string]any) {
		p["IsDisabled"] = false
		p["InvalidLoginAttemptCount"] = 0
	})
	if code, _ := sessReq(t, ts.URL, "GET", "/Users/Me", "Phone", "d1", tok, nil); code != http.StatusOK {
		t.Fatalf("token after re-enable = %d, want 200", code)
	}
	for _, e := range recentActivity(t, st, 100) {
		if len(e) >= 12 && e[:12] == "SessionEnded" {
			t.Fatalf("unexpected %q", e)
		}
	}
}

func TestReloginWritesSessionEndedWithOldDeviceName(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	sessLogin(t, ts.URL, "alice", "pw", "Phone", "d1")
	sessLogin(t, ts.URL, "alice", "pw", "Tablet", "d2")
	for _, e := range recentActivity(t, st, 100) {
		if len(e) >= 12 && e[:12] == "SessionEnded" {
			t.Fatalf("unexpected %q", e)
		}
	}
	sessLogin(t, ts.URL, "alice", "pw", "Phone 2", "d1")
	got := recentActivity(t, st, 3)
	prefix := []string{"AuthenticationSucceeded|alice successfully authenticated|", "SessionStarted|alice is online from Phone 2|", "SessionEnded|alice has disconnected from Phone|IP address: "}
	for i, w := range prefix {
		if len(got[i]) < len(w) || got[i][:len(w)] != w {
			t.Fatalf("activity[%d] = %q, want prefix %q", i, got[i], w)
		}
	}
	if got[2] == "SessionEnded|alice has disconnected from Phone|IP address: " {
		t.Fatalf("session ended entry has no address: %q", got[2])
	}
}

func TestLogoutRevokesTokenAndWritesSessionEnded(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	_, ta := sessLogin(t, ts.URL, "alice", "pw", "Phone", "d1")
	_, tb := sessLogin(t, ts.URL, "alice", "pw", "Tablet", "d2")

	if code, _ := sessReq(t, ts.URL, "POST", "/Sessions/Logout", "Phone", "d1", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous logout = %d, want 401", code)
	}
	if code, _ := sessReq(t, ts.URL, "POST", "/Sessions/Logout", "Phone", "d1", "ffffffffffffffffffffffffffffffff", nil); code != http.StatusUnauthorized {
		t.Fatalf("unknown token logout = %d, want 401", code)
	}
	if code, body := sessReq(t, ts.URL, "POST", "/Sessions/Logout", "Phone", "d1", ta, nil); code != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("logout = %d %q, want 204 empty", code, body)
	}
	if code, _ := sessReq(t, ts.URL, "GET", "/Users/Me", "Phone", "d1", ta, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d", code)
	}
	if code, _ := sessReq(t, ts.URL, "GET", "/Users/Me", "Tablet", "d2", tb, nil); code != http.StatusOK {
		t.Fatalf("other token = %d", code)
	}
	got := recentActivity(t, st, 1)[0]
	if len(got) < 53 || got[:53] != "SessionEnded|alice has disconnected from Phone|IP add" {
		t.Fatalf("newest = %q", got)
	}
	if code, _ := sessReq(t, ts.URL, "POST", "/Sessions/Logout", "Phone", "d1", ta, nil); code != http.StatusUnauthorized {
		t.Fatalf("second logout = %d, want 401", code)
	}
}

func TestPasswordChangeActivityEntries(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")
	sessLogin(t, ts.URL, "alice", "pw", "One", "d1")
	sessLogin(t, ts.URL, "alice", "pw", "Two", "d2")

	code, out := sessReq(t, ts.URL, "POST", "/Users/Password?userId="+string(u.ID), "Adm", "adm-dev", adminTok, map[string]any{"NewPw": "pw2"})
	if code != http.StatusNoContent {
		t.Fatalf("admin set password = %d %s", code, out)
	}
	got := recentActivity(t, st, 3)
	if got[2] != "UserPasswordChanged|Password has been changed for user alice|" {
		t.Fatalf("activity = %q", got)
	}
	if got[1][:34] != "SessionEnded|alice has disconnecte" || got[0][:12] != "SessionEnded" {
		t.Fatalf("activity = %q", got)
	}
	if got[1][:50] == got[0][:50] {
		t.Fatalf("entries not distinct: %q", got)
	}
	// oldest token ended first, so newest-first listing starts with device 2.
	if want := "SessionEnded|alice has disconnected from Two|"; got[0][:len(want)] != want {
		t.Fatalf("newest = %q, want device Two", got[0])
	}

	// Wrong current password: nothing written. Reset: password entry only.
	_, tok := sessLogin(t, ts.URL, "alice", "pw2", "One", "d1")
	n := len(recentActivity(t, st, 100))
	if code, _ := sessReq(t, ts.URL, "POST", "/Users/Password", "One", "d1", tok, map[string]any{"CurrentPw": "nope", "NewPw": "x"}); code != http.StatusForbidden {
		t.Fatalf("wrong current = %d", code)
	}
	if m := len(recentActivity(t, st, 100)); m != n {
		t.Fatalf("refusal wrote %d entries", m-n)
	}
	if code, _ := sessReq(t, ts.URL, "POST", "/Users/Password?userId="+string(u.ID), "Adm", "adm-dev", adminTok, map[string]any{"ResetPassword": true}); code != http.StatusNoContent {
		t.Fatalf("reset = %d", code)
	}
	wantActivity(t, st, "UserPasswordChanged|Password has been changed for user alice|")
}

func TestWrongCurrentPwLockoutWritesOnlyUserLockedOut(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, func(p map[string]any) { p["LoginAttemptsBeforeLockout"] = 2 })
	_, tok := sessLogin(t, ts.URL, "alice", "pw", "One", "d1")
	for i := 0; i < 2; i++ {
		sessReq(t, ts.URL, "POST", "/Users/Password", "One", "d1", tok, map[string]any{"CurrentPw": "nope", "NewPw": "x"})
	}
	got := recentActivity(t, st, 2)
	if got[0] != "UserLockedOut|User alice has been locked out|" || got[1][:23] != "AuthenticationSucceeded" {
		t.Fatalf("activity = %q", got)
	}
}

func TestPolicyRevocationsWriteSessionEnded(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	u := createTestUser(t, st, "alice", "pw")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")
	sessLogin(t, ts.URL, "alice", "pw", "One", "d1")
	sessLogin(t, ts.URL, "alice", "pw", "Two", "d2")

	restrict := func(p map[string]any) {
		p["EnableAllDevices"] = false
		p["EnabledDevices"] = []string{"d2"}
	}
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, restrict)
	if got := recentActivity(t, st, 1)[0]; got[:44] != "SessionEnded|alice has disconnected from One" {
		t.Fatalf("newest = %q", got)
	}
	n := len(recentActivity(t, st, 100))
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, restrict)
	if m := len(recentActivity(t, st, 100)); m != n {
		t.Fatalf("policy update wrote %d entries", m-n)
	}
	sessSetPolicy(t, ts.URL, adminTok, string(u.ID), st, func(p map[string]any) { p["IsDisabled"] = true })
	if got := recentActivity(t, st, 1)[0]; got[:44] != "SessionEnded|alice has disconnected from Two" {
		t.Fatalf("newest = %q", got)
	}
}

func TestCreateAndDeleteUserActivityEntries(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	_, adminTok := sessLogin(t, ts.URL, admin.Name, "adminpass", "Adm", "adm-dev")

	create := func(body map[string]any) string {
		code, out := sessReq(t, ts.URL, "POST", "/Users/New", "Adm", "adm-dev", adminTok, body)
		if code != http.StatusOK {
			t.Fatalf("create = %d %s", code, out)
		}
		var u struct{ Id string }
		_ = json.Unmarshal(out, &u)
		return u.Id
	}
	create(map[string]any{"Name": "bob"})
	wantActivity(t, st, "UserCreated|User bob has been created|")
	create(map[string]any{"Name": "carol", "Password": ""})
	wantActivity(t, st, "UserPasswordChanged|Password has been changed for user carol|", "UserCreated|User carol has been created|")

	id := create(map[string]any{"Name": "dave", "Password": "pw"})
	sessLogin(t, ts.URL, "dave", "pw", "One", "d1")
	sessLogin(t, ts.URL, "dave", "pw", "Two", "d2")
	if code, _ := sessReq(t, ts.URL, "DELETE", "/Users/"+id, "Adm", "adm-dev", adminTok, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	wantActivity(t, st, "UserDeleted|User dave has been deleted|")
	entries, _, _ := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 3})
	if entries[0].UserID != nil || entries[0].Severity != storage.ActivitySeverityInfo {
		t.Fatalf("UserDeleted entry = %+v", entries[0])
	}
	if entries[1].Type != "SessionEnded" || entries[2].Type != "SessionEnded" || entries[1].Name != "dave has disconnected from Two" {
		t.Fatalf("entries = %+v", entries)
	}
}
