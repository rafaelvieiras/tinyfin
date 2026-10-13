package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// policyDo issues requests with a MediaBrowser Authorization header
// carrying the given token.
func policyDo(t *testing.T, baseURL, token, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Client="Test", Device="Test", DeviceId="policy-test", Version="1.0.0", Token="`+token+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func policyLogin(t *testing.T, baseURL, name, pw string) string {
	t.Helper()
	resp := authenticate(t, baseURL, name, pw)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d", name, resp.StatusCode)
	}
	var res authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}
	return res.AccessToken
}

func policyOf(t *testing.T, baseURL, token, userID string) userPolicyDto {
	t.Helper()
	code, body := policyDo(t, baseURL, token, http.MethodGet, "/Users/"+userID, "")
	if code != http.StatusOK {
		t.Fatalf("GET user: status %d", code)
	}
	var u userDto
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decoding user: %v", err)
	}
	return u.Policy
}

const providers = `"AuthenticationProviderId":"p","PasswordResetProviderId":"r"`

func TestUserPolicyRoundTrip(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	body := `{` + providers + `,"IsHidden":false,"EnableAllFolders":false,"EnabledFolders":["f1"],
		"BlockedTags":["a","b"],"AllowedTags":["c"],"EnableContentDownloading":false,"EnablePlaybackRemuxing":false,
		"EnableContentDeletion":true,"EnableRemoteControlOfOtherUsers":true,"MaxActiveSessions":3,
		"LoginAttemptsBeforeLockout":5,"SyncPlayAccess":"JoinGroups","MaxParentalRating":7,
		"AccessSchedules":[{"DayOfWeek":"Monday","StartHour":8,"EndHour":18}]}`
	code, resp := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy", body)
	if code != http.StatusNoContent || len(resp) != 0 {
		t.Fatalf("POST policy: status %d body %q, want 204 empty", code, resp)
	}

	p := policyOf(t, ts.URL, tok, string(target.ID))
	if p.IsHidden || p.EnableAllFolders || p.EnableContentDownloading || p.EnablePlaybackRemuxing ||
		!p.EnableContentDeletion || !p.EnableRemoteControlOfOtherUsers || p.MaxActiveSessions != 3 ||
		p.LoginAttemptsBeforeLockout != 5 || p.SyncPlayAccess != "JoinGroups" {
		t.Errorf("scalar policy fields not persisted: %+v", p)
	}
	if len(p.EnabledFolders) != 1 || p.EnabledFolders[0] != "f1" || len(p.BlockedTags) != 2 || len(p.AllowedTags) != 1 {
		t.Errorf("list fields not persisted: %+v", p)
	}
	if p.MaxParentalRating == nil || *p.MaxParentalRating != 7 {
		t.Errorf("MaxParentalRating = %v, want 7", p.MaxParentalRating)
	}
	if len(p.AccessSchedules) != 1 || p.AccessSchedules[0].DayOfWeek != "Monday" || p.AccessSchedules[0].EndHour != 18 {
		t.Errorf("AccessSchedules = %+v", p.AccessSchedules)
	}

	// Full replacement: a minimal body resets everything else to the
	// defaults of a new account.
	code, _ = policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy", `{`+providers+`}`)
	if code != http.StatusNoContent {
		t.Fatalf("POST minimal policy: status %d", code)
	}
	p = policyOf(t, ts.URL, tok, string(target.ID))
	def := defaultUserPolicy()
	if p.IsHidden != def.IsHidden || !p.EnableAllFolders || p.MaxActiveSessions != 0 || len(p.BlockedTags) != 0 ||
		p.LoginAttemptsBeforeLockout != -1 || p.MaxParentalRating != nil || p.AuthenticationProviderId != "p" {
		t.Errorf("policy not replaced as a whole: %+v", p)
	}
}

func TestUserPolicyValidation(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	target := createTestUser(t, st, "target", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")
	path := "/Users/" + string(target.ID) + "/Policy"

	cases := []struct {
		name, tok, path, body string
		want                  int
	}{
		{"anonymous", "", path, `{` + providers + `}`, http.StatusUnauthorized},
		{"non-admin on other", aliceTok, path, `{` + providers + `}`, http.StatusForbidden},
		{"non-admin on self", aliceTok, "/Users/" + string(alice.ID) + "/Policy", `{` + providers + `}`, http.StatusForbidden},
		{"unknown user", adminTok, "/Users/00000000-0000-0000-0000-000000000001/Policy", `{` + providers + `}`, http.StatusNotFound},
		{"not json", adminTok, path, `nope`, http.StatusBadRequest},
		{"providers missing", adminTok, path, `{"IsHidden":false}`, http.StatusBadRequest},
		{"empty auth provider", adminTok, path, `{"AuthenticationProviderId":"","PasswordResetProviderId":"r"}`, http.StatusBadRequest},
		{"empty reset provider", adminTok, path, `{"AuthenticationProviderId":"p","PasswordResetProviderId":""}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if code, _ := policyDo(t, ts.URL, c.tok, http.MethodPost, c.path, c.body); code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, code, c.want)
		}
	}

	// Nothing changed by the rejected requests.
	if p := policyOf(t, ts.URL, adminTok, string(target.ID)); p.AuthenticationProviderId == "p" {
		t.Errorf("rejected request changed the policy: %+v", p)
	}
}

func TestUserPolicyAdministratorRules(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	adminPath := "/Users/" + string(admin.ID) + "/Policy"

	// R6: the only administrator cannot be demoted.
	code, _ := policyDo(t, ts.URL, tok, http.MethodPost, adminPath, `{`+providers+`,"IsAdministrator":false}`)
	if code != http.StatusForbidden {
		t.Errorf("demote only admin: status %d, want 403", code)
	}
	// R7: an administrator cannot be disabled (also while demoted).
	for _, b := range []string{`{` + providers + `,"IsAdministrator":true,"IsDisabled":true}`, `{` + providers + `,"IsAdministrator":false,"IsDisabled":true}`} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, adminPath, b); code != http.StatusForbidden {
			t.Errorf("disable admin %s: status %d, want 403", b, code)
		}
	}
	if p := policyOf(t, ts.URL, tok, string(admin.ID)); !p.IsAdministrator || p.IsDisabled {
		t.Errorf("admin changed by rejected requests: %+v", p)
	}

	// R10: with a second administrator, self-demotion works, and takes
	// effect on the existing token immediately (R9).
	second := createTestUser(t, st, "second", "pw")
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(second.ID)+"/Policy", `{`+providers+`,"IsAdministrator":true}`); code != http.StatusNoContent {
		t.Fatalf("promote: status %d", code)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, adminPath, `{`+providers+`,"IsAdministrator":false}`); code != http.StatusNoContent {
		t.Fatalf("self-demotion: status %d, want 204", code)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, adminPath, `{`+providers+`,"IsAdministrator":true}`); code != http.StatusForbidden {
		t.Errorf("demoted admin's token still administrator: status %d, want 403", code)
	}
}

func TestUserPolicyDisableRevokesTokens(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	targetTok := policyLogin(t, ts.URL, "target", "pw")

	if code, _ := policyDo(t, ts.URL, targetTok, http.MethodGet, "/Users/Me", ""); code != http.StatusOK {
		t.Fatalf("target token before disable: %d", code)
	}
	code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy", `{`+providers+`,"IsDisabled":true}`)
	if code != http.StatusNoContent {
		t.Fatalf("disable: status %d", code)
	}
	if code, _ := policyDo(t, ts.URL, targetTok, http.MethodGet, "/Users/Me", ""); code != http.StatusUnauthorized {
		t.Errorf("token of disabled user: status %d, want 401", code)
	}
	if _, err := st.LookupToken(context.Background(), targetTok); err == nil {
		t.Errorf("token still stored after disabling the account")
	}
}

func TestUserPolicyNewUserDefaultsAndPublicList(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	code, body := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", `{"Name":"fresh","Password":"pw"}`)
	if code != http.StatusOK {
		t.Fatalf("create: status %d", code)
	}
	var u userDto
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatal(err)
	}
	p := u.Policy
	if p.IsAdministrator || p.IsDisabled || !p.IsHidden || !p.EnableAllFolders || p.LoginAttemptsBeforeLockout != -1 ||
		p.SyncPlayAccess != "CreateAndJoinGroups" || p.AuthenticationProviderId == "" || p.PasswordResetProviderId == "" {
		t.Errorf("unexpected default policy: %+v", p)
	}

	names := func() []string {
		_, b := policyDo(t, ts.URL, "", http.MethodGet, "/Users/Public", "")
		var us []userDto
		_ = json.Unmarshal(b, &us)
		var out []string
		for _, x := range us {
			out = append(out, x.Name)
		}
		return out
	}
	if got := names(); len(got) != 1 || got[0] != "admin" {
		t.Errorf("public list with hidden user = %v, want [admin]", got)
	}
	policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+u.ID+"/Policy", `{`+providers+`,"IsHidden":false}`)
	if got := names(); len(got) != 2 {
		t.Errorf("public list after unhiding = %v, want 2 entries", got)
	}
}

func TestUserPolicyRevokesUnlistedDeviceTokens(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	target := createTestUser(t, st, "target", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	ctx := context.Background()
	keep, _ := st.IssueToken(ctx, target.ID, "dev-keep")
	drop, _ := st.IssueToken(ctx, target.ID, "dev-drop")

	b, _ := json.Marshal(map[string]any{"AuthenticationProviderId": "p", "PasswordResetProviderId": "r",
		"EnableAllDevices": false, "EnabledDevices": []string{"dev-keep"}})
	code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(target.ID)+"/Policy", string(b))
	if code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	if _, err := st.LookupToken(ctx, keep.Token); err != nil {
		t.Errorf("allowed device token revoked: %v", err)
	}
	if _, err := st.LookupToken(ctx, drop.Token); err == nil {
		t.Errorf("disallowed device token kept")
	}
}
