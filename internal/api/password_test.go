package api

import (
	"net/http"
	"net/url"
	"testing"
)

// pwReq posts a password change as the holder of token; target is the
// optional userId query, path an optional alias path id.
func pwReq(t *testing.T, base, token, target, body string) int {
	t.Helper()
	p := "/Users/Password"
	if target != "" {
		p += "?userId=" + url.QueryEscape(target)
	}
	code, _ := policyDo(t, base, token, http.MethodPost, p, body)
	return code
}

func loginOK(t *testing.T, base, name, pw string) bool {
	t.Helper()
	resp := authenticate(t, base, name, pw)
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func TestPasswordChangeOwn(t *testing.T) {
	ts, st := newTestServer(t)
	u := createTestUser(t, st, "alice", "old-pw")
	tok := policyLogin(t, ts.URL, "alice", "old-pw")

	// users.password.R2: wrong, missing or legacy-hash-only current password.
	for _, body := range []string{
		`{"CurrentPw":"nope","NewPw":"x"}`,
		`{"NewPw":"x"}`,
		`{"CurrentPassword":"old-pw","NewPw":"x"}`,
	} {
		if code := pwReq(t, ts.URL, tok, "", body); code != http.StatusForbidden {
			t.Errorf("body %s: status %d, want 403", body, code)
		}
	}
	if !loginOK(t, ts.URL, "alice", "old-pw") {
		t.Fatal("password changed by a refused request")
	}
	tok = policyLogin(t, ts.URL, "alice", "old-pw")
	if code := pwReq(t, ts.URL, tok, string(u.ID), `{"CurrentPw":"old-pw","NewPw":"new-pw"}`); code != http.StatusNoContent {
		t.Fatalf("change own: status %d, want 204", code)
	}
	if loginOK(t, ts.URL, "alice", "old-pw") || !loginOK(t, ts.URL, "alice", "new-pw") {
		t.Error("password was not replaced")
	}
}

func TestPasswordRemoveAndSetOnPasswordless(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	tok := policyLogin(t, ts.URL, "alice", "pw")
	// users.password.R5: empty NewPw removes the password.
	if code := pwReq(t, ts.URL, tok, "", `{"CurrentPw":"pw","NewPw":""}`); code != http.StatusNoContent {
		t.Fatalf("remove: status %d", code)
	}
	if !loginOK(t, ts.URL, "alice", "") {
		t.Error("empty password login refused after removal")
	}
	tok = policyLogin(t, ts.URL, "alice", "")
	// An account without a password needs no CurrentPw.
	if code := pwReq(t, ts.URL, tok, "", `{"NewPw":"fresh"}`); code != http.StatusNoContent {
		t.Fatalf("set on passwordless: status %d", code)
	}
	if !loginOK(t, ts.URL, "alice", "fresh") {
		t.Error("new password refused")
	}
}

func TestPasswordRevokesOtherTokens(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	// Two devices for alice (policyDo's DeviceId is fixed, so log in with
	// distinct ones directly).
	login := func(device string) string {
		return deviceLogin(t, ts.URL, "alice", "pw", device)
	}
	tokA, tokB := login("dev-a"), login("dev-b")
	if code := pwReq(t, ts.URL, tokA, "", `{"CurrentPw":"pw","NewPw":"pw2"}`); code != http.StatusNoContent {
		t.Fatalf("change: status %d", code)
	}
	if code, _ := policyDo(t, ts.URL, tokA, http.MethodGet, "/Users/Me", ""); code != http.StatusOK {
		t.Errorf("current device: status %d, want 200", code)
	}
	if code, _ := policyDo(t, ts.URL, tokB, http.MethodGet, "/Users/Me", ""); code != http.StatusUnauthorized {
		t.Errorf("other device: status %d, want 401", code)
	}

	// users.password.R3/R7: an administrator sets another user's password
	// without CurrentPw and every token of the target is revoked.
	tokC := deviceLogin(t, ts.URL, "alice", "pw2", "dev-c")
	admin := policyLogin(t, ts.URL, "admin", "adminpw")
	alice, _ := st.GetUserByName(t.Context(), "alice")
	if code := pwReq(t, ts.URL, admin, string(alice.ID), `{"NewPw":"pw3"}`); code != http.StatusNoContent {
		t.Fatalf("admin sets: status %d", code)
	}
	if code, _ := policyDo(t, ts.URL, tokC, http.MethodGet, "/Users/Me", ""); code != http.StatusUnauthorized {
		t.Errorf("target token after admin change: status %d, want 401", code)
	}
}

func TestPasswordResetPassword(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestUser(t, st, "bob", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	alice, _ := st.GetUserByName(t.Context(), "alice")
	bob, _ := st.GetUserByName(t.Context(), "bob")
	tok := policyLogin(t, ts.URL, "alice", "pw")
	admin := policyLogin(t, ts.URL, "admin", "adminpw")

	// users.password.R4: a non-admin may not reset someone else.
	if code := pwReq(t, ts.URL, tok, string(bob.ID), `{"ResetPassword":true}`); code != http.StatusForbidden {
		t.Errorf("reset other: status %d, want 403", code)
	}
	// users.password.R9: own reset needs no CurrentPw and keeps sessions.
	if code := pwReq(t, ts.URL, tok, "", `{"ResetPassword":true}`); code != http.StatusNoContent {
		t.Fatalf("reset own: status %d", code)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/Me", ""); code != http.StatusOK {
		t.Errorf("token after reset: status %d, want 200", code)
	}
	if !loginOK(t, ts.URL, "alice", "") {
		t.Error("login with empty password refused after reset")
	}
	// Admin resets another user.
	if code := pwReq(t, ts.URL, admin, string(bob.ID), `{"ResetPassword":true}`); code != http.StatusNoContent {
		t.Fatalf("admin reset: status %d", code)
	}
	_ = alice
	// users.password.R8: an administrator never ends up without a password.
	adm, _ := st.GetUserByName(t.Context(), "admin")
	for _, body := range []string{`{"ResetPassword":true}`, `{"NewPw":""}`, `{"CurrentPw":"adminpw"}`} {
		if code := pwReq(t, ts.URL, admin, "", body); code != http.StatusBadRequest {
			t.Errorf("admin self body %s: status %d, want 400", body, code)
		}
	}
	if !loginOK(t, ts.URL, "admin", "adminpw") {
		t.Error("admin password changed by a refused request")
	}
	_ = adm
}

func TestPasswordAdminSelfRules(t *testing.T) {
	ts, st := newTestServer(t)
	adm := createTestAdminUser(t, st, "admin", "adminpw")
	tok := policyLogin(t, ts.URL, "admin", "adminpw")
	// users.password.R6: no userId means no CurrentPw needed...
	if code := pwReq(t, ts.URL, tok, "", `{"NewPw":"pw2"}`); code != http.StatusNoContent {
		t.Fatalf("admin self without userId: status %d", code)
	}
	// ...but an explicit own userId requires it.
	if code := pwReq(t, ts.URL, tok, string(adm.ID), `{"NewPw":"pw3"}`); code != http.StatusForbidden {
		t.Errorf("admin self with userId, no CurrentPw: status %d, want 403", code)
	}
	if code := pwReq(t, ts.URL, tok, string(adm.ID), `{"CurrentPw":"pw2","NewPw":"pw3"}`); code != http.StatusNoContent {
		t.Errorf("admin self with userId and CurrentPw: status %d, want 204", code)
	}
	// users.password.R14: the alias always requires it.
	code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(adm.ID)+"/Password", `{"NewPw":"pw4"}`)
	if code != http.StatusForbidden {
		t.Errorf("alias without CurrentPw: status %d, want 403", code)
	}
	code, _ = policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(adm.ID)+"/Password", `{"CurrentPw":"pw3","NewPw":"pw4"}`)
	if code != http.StatusNoContent {
		t.Errorf("alias with CurrentPw: status %d, want 204", code)
	}
}

func TestPasswordErrors(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	alice, _ := st.GetUserByName(t.Context(), "alice")
	tok := policyLogin(t, ts.URL, "alice", "pw")
	admin := policyLogin(t, ts.URL, "admin", "adminpw")

	if code := pwReq(t, ts.URL, "", string(alice.ID), `{"NewPw":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", code)
	}
	if code := pwReq(t, ts.URL, admin, unknownUserID, `{"NewPw":"x"}`); code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", code)
	}
	if code := pwReq(t, ts.URL, admin, "not-a-guid", `{"NewPw":"x"}`); code != http.StatusBadRequest {
		t.Errorf("bad id: status %d, want 400", code)
	}
	if code := pwReq(t, ts.URL, tok, "", `not json`); code != http.StatusBadRequest {
		t.Errorf("bad body: status %d, want 400", code)
	}
	adm, _ := st.GetUserByName(t.Context(), "admin")
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(adm.ID)+"/Password", `{"CurrentPw":"x","NewPw":"y"}`); code != http.StatusForbidden {
		t.Errorf("alias naming another user: status %d, want 403", code)
	}
}

func TestPasswordPreferenceAccessDisabled(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	alice, _ := st.GetUserByName(t.Context(), "alice")
	admin := policyLogin(t, ts.URL, "admin", "adminpw")
	tok := policyLogin(t, ts.URL, "alice", "pw")
	code, _ := policyDo(t, ts.URL, admin, http.MethodPost, "/Users/"+string(alice.ID)+"/Policy",
		`{"AuthenticationProviderId":"Tinyfin.Auth.Default","PasswordResetProviderId":"Tinyfin.Auth.Default","EnableUserPreferenceAccess":false}`)
	if code != http.StatusNoContent {
		t.Fatalf("policy: status %d", code)
	}
	if code := pwReq(t, ts.URL, tok, "", `{"CurrentPw":"pw","NewPw":"x"}`); code != http.StatusForbidden {
		t.Errorf("status %d, want 403", code)
	}
}

func TestPasswordWrongCurrentCountsTowardLockout(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	alice, _ := st.GetUserByName(t.Context(), "alice")
	admin := policyLogin(t, ts.URL, "admin", "adminpw")
	tok := policyLogin(t, ts.URL, "alice", "pw")
	code, _ := policyDo(t, ts.URL, admin, http.MethodPost, "/Users/"+string(alice.ID)+"/Policy",
		`{"AuthenticationProviderId":"Tinyfin.Auth.Default","PasswordResetProviderId":"Tinyfin.Auth.Default","LoginAttemptsBeforeLockout":2}`)
	if code != http.StatusNoContent {
		t.Fatalf("policy: status %d", code)
	}
	for i := 0; i < 2; i++ {
		if code := pwReq(t, ts.URL, tok, "", `{"CurrentPw":"bad","NewPw":"x"}`); code != http.StatusForbidden {
			t.Fatalf("attempt %d: status %d, want 403", i, code)
		}
	}
	if loginOK(t, ts.URL, "alice", "pw") {
		t.Error("account not locked out after repeated wrong CurrentPw")
	}
}
