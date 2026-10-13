package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const unknownUserID = "00000000-0000-0000-0000-000000000001"

func getUser(t *testing.T, baseURL, token, id string) userDto {
	t.Helper()
	code, body := policyDo(t, baseURL, token, http.MethodGet, "/Users/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET /Users/%s: status %d", id, code)
	}
	var u userDto
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decoding user: %v", err)
	}
	return u
}

func TestUserCreateNameRules(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "Alice", "pw")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	for _, name := range []string{"", " lead", "trail ", ".", "..", "a/b", "a*b", "a?b", "a<b>"} {
		b, _ := json.Marshal(map[string]string{"Name": name, "Password": "x"})
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", string(b)); code != http.StatusBadRequest {
			t.Errorf("create %q: status %d, want 400", name, code)
		}
	}
	for _, body := range []string{`{}`, `not json`, `{"Name":5}`} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", body); code != http.StatusBadRequest {
			t.Errorf("create body %q: status %d, want 400", body, code)
		}
	}
	for _, name := range []string{"ALICE", "alice", "admin"} {
		b, _ := json.Marshal(map[string]string{"Name": name})
		if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", string(b)); code != http.StatusBadRequest {
			t.Errorf("duplicate %q: status %d, want 400", name, code)
		}
	}
	// Non-ASCII names deliberately exercise the letter classes of the name rule.
	for _, name := range []string{"Zoë Müller", "a.b-c_d'e@f+g", "用户"} {
		b, _ := json.Marshal(map[string]string{"Name": name})
		if code, body := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", string(b)); code != http.StatusOK {
			t.Errorf("create %q: status %d body %s, want 200", name, code, body)
		}
	}
}

func TestUserHasPasswordFlag(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	mk := func(body string) userDto {
		code, resp := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", body)
		if code != http.StatusOK {
			t.Fatalf("create %s: status %d", body, code)
		}
		var u userDto
		_ = json.Unmarshal(resp, &u)
		return getUser(t, ts.URL, tok, u.ID)
	}
	if u := mk(`{"Name":"withpw","Password":"secret"}`); !u.HasPassword || !u.HasConfiguredPassword {
		t.Errorf("account with password: HasPassword=%v HasConfiguredPassword=%v", u.HasPassword, u.HasConfiguredPassword)
	}
	if u := mk(`{"Name":"nopw"}`); u.HasPassword || u.HasConfiguredPassword {
		t.Errorf("account without password: HasPassword=%v HasConfiguredPassword=%v", u.HasPassword, u.HasConfiguredPassword)
	}
}

func TestUserGetBadIDs(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	for id, want := range map[string]int{
		"00000000-0000-0000-0000-000000000000": http.StatusBadRequest,
		"not-a-guid":                           http.StatusBadRequest,
		unknownUserID:                          http.StatusNotFound,
	} {
		if code, _ := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/"+id, ""); code != want {
			t.Errorf("GET /Users/%s: status %d, want %d", id, code, want)
		}
	}
}

func TestUserListFilters(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")
	visible := createTestUser(t, st, "visible", "pw") // no stored policy: not hidden
	disabled := createTestUser(t, st, "disabled", "pw")
	if code, _ := policyDo(t, ts.URL, tok, http.MethodPost, "/Users/"+string(disabled.ID)+"/Policy",
		`{`+providers+`,"IsDisabled":true,"IsHidden":true}`); code != http.StatusNoContent {
		t.Fatalf("policy: %d", code)
	}
	ids := func(query string) map[string]bool {
		code, body := policyDo(t, ts.URL, tok, http.MethodGet, "/Users"+query, "")
		if code != http.StatusOK {
			t.Fatalf("GET /Users%s: status %d", query, code)
		}
		var us []userDto
		_ = json.Unmarshal(body, &us)
		m := map[string]bool{}
		for _, u := range us {
			m[u.ID] = true
		}
		return m
	}
	if m := ids(""); len(m) != 3 {
		t.Errorf("unfiltered: %v", m)
	}
	if m := ids("?isHidden=true"); len(m) != 1 || !m[string(disabled.ID)] {
		t.Errorf("isHidden=true: %v", m)
	}
	if m := ids("?isHidden=false"); len(m) != 2 || !m[string(visible.ID)] || !m[string(admin.ID)] {
		t.Errorf("isHidden=false: %v", m)
	}
	if m := ids("?isDisabled=true"); len(m) != 1 || !m[string(disabled.ID)] {
		t.Errorf("isDisabled=true: %v", m)
	}
	if m := ids("?isDisabled=false"); len(m) != 2 || m[string(disabled.ID)] {
		t.Errorf("isDisabled=false: %v", m)
	}
	if m := ids("?isDisabled=true&isHidden=false"); len(m) != 0 {
		t.Errorf("combined: %v", m)
	}
	if code, _ := policyDo(t, ts.URL, tok, http.MethodGet, "/Users?isHidden=maybe", ""); code != http.StatusBadRequest {
		t.Errorf("invalid bool: status %d, want 400", code)
	}
}

func updateBody(name string, extra string) string {
	b, _ := json.Marshal(name)
	s := `{"Name":` + string(b) + `,"Policy":{` + providers + `}`
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

func TestUserUpdate(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	bob := createTestUser(t, st, "bob", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")
	post := func(tok, path, body string) int {
		code, _ := policyDo(t, ts.URL, tok, http.MethodPost, path, body)
		return code
	}
	aid := string(alice.ID)

	// Self update: rename and preferences, hostile policy ignored.
	body := updateBody("alice2", `"Configuration":{"SubtitleMode":"Always","AudioLanguagePreference":"por","PlayDefaultAudioTrack":false},"Id":"`+string(bob.ID)+`"`)
	body = strings.Replace(body, `"Policy":{`, `"Policy":{"IsAdministrator":true,`, 1)
	if code := post(aliceTok, "/Users", body); code != http.StatusNoContent {
		t.Fatalf("self update: %d", code)
	}
	u := getUser(t, ts.URL, adminTok, aid)
	if u.Name != "alice2" || u.Configuration.SubtitleMode != "Always" || derefStr(u.Configuration.AudioLanguagePreference) != "por" ||
		u.Configuration.PlayDefaultAudioTrack || u.Policy.IsAdministrator || !u.HasPassword {
		t.Errorf("after self update: %+v", u)
	}

	// Case-only rename and same-name no-op.
	for _, n := range []string{"ALICE2", "ALICE2"} {
		if code := post(adminTok, "/Users?userId="+aid, updateBody(n, "")); code != http.StatusNoContent {
			t.Errorf("rename %q: %d", n, code)
		}
	}
	if u := getUser(t, ts.URL, adminTok, aid); u.Name != "ALICE2" {
		t.Errorf("name = %q", u.Name)
	}
	// Omitted Configuration resets preferences.
	if u := getUser(t, ts.URL, adminTok, aid); u.Configuration.SubtitleMode != "Default" || !u.Configuration.PlayDefaultAudioTrack {
		t.Errorf("configuration not reset: %+v", u.Configuration)
	}

	// Validation.
	for _, c := range []struct {
		name, tok, path, body string
		want                  int
	}{
		{"duplicate", adminTok, "/Users?userId=" + aid, updateBody("BOB", ""), 400},
		{"invalid", adminTok, "/Users?userId=" + aid, updateBody("bad/name", ""), 400},
		{"empty", adminTok, "/Users?userId=" + aid, updateBody("", ""), 400},
		{"missing name", adminTok, "/Users?userId=" + aid, `{"Policy":{` + providers + `}}`, 400},
		{"missing policy", adminTok, "/Users?userId=" + unknownUserID, `{"Name":"x"}`, 400},
		{"policy no ids", adminTok, "/Users", `{"Name":"admin","Policy":{}}`, 400},
		{"policy one id", adminTok, "/Users", `{"Name":"admin","Policy":{"AuthenticationProviderId":"p"}}`, 400},
		{"not json", adminTok, "/Users", `nope`, 400},
		{"no body", adminTok, "/Users", ``, 400},
		{"bad enum", adminTok, "/Users", updateBody("admin", `"Configuration":{"SubtitleMode":"Nope"}`), 400},
		{"bad user id", adminTok, "/Users?userId=zzz", updateBody("x", ""), 400},
		{"empty user id", adminTok, "/Users?userId=00000000-0000-0000-0000-000000000000", updateBody("x", ""), 400},
		{"unknown user", adminTok, "/Users?userId=" + unknownUserID, updateBody("ghost", ""), 404},
		{"non-admin other", aliceTok, "/Users?userId=" + string(bob.ID), updateBody("bobby", ""), 403},
		{"anonymous", "", "/Users?userId=" + aid, updateBody("x", ""), 401},
		{"empty configuration object", adminTok, "/Users?userId=" + aid, updateBody("ALICE2", `"Configuration":{}`), 204},
		{"null policy", adminTok, "/Users?userId=" + aid, `{"Name":"ALICE2","Policy":null}`, 204},
		{"alias admin", adminTok, "/Users/" + aid, updateBody("alice3", ""), 204},
		{"alias non-admin", aliceTok, "/Users/" + string(bob.ID), updateBody("bobby", ""), 403},
		{"alias unknown", adminTok, "/Users/" + unknownUserID, updateBody("ghost", ""), 404},
		{"alias self", aliceTok, "/Users/" + aid, updateBody("alice3", `"Configuration":{"SubtitleMode":"OnlyForced"}`), 204},
		{"alias bad policy", adminTok, "/Users/" + aid, `{"Name":"alice3"}`, 400},
	} {
		if code := post(c.tok, c.path, c.body); code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, code, c.want)
		}
	}
	if u := getUser(t, ts.URL, adminTok, aid); u.Name != "alice3" || u.Configuration.SubtitleMode != "OnlyForced" {
		t.Errorf("final state: %+v", u)
	}
	if u := getUser(t, ts.URL, adminTok, string(bob.ID)); u.Name != "bob" {
		t.Errorf("bob renamed: %q", u.Name)
	}
}

func TestUserUpdateRequiresPreferenceAccess(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")
	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users/"+string(alice.ID)+"/Policy",
		`{`+providers+`,"EnableUserPreferenceAccess":false}`); code != http.StatusNoContent {
		t.Fatalf("policy: %d", code)
	}
	if code, _ := policyDo(t, ts.URL, aliceTok, http.MethodPost, "/Users", updateBody("alice", "")); code != http.StatusForbidden {
		t.Errorf("self update without preference access: %d, want 403", code)
	}
	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users?userId="+string(alice.ID), updateBody("alice", "")); code != http.StatusNoContent {
		t.Errorf("admin update: %d, want 204", code)
	}
}

func TestUserConfiguration(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	bob := createTestUser(t, st, "bob", "pw")
	carol := createTestUser(t, st, "carol", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")
	aid, bid, cid := string(alice.ID), string(bob.ID), string(carol.ID)
	post := func(tok, path, body string) int {
		code, resp := policyDo(t, ts.URL, tok, http.MethodPost, path, body)
		if code == http.StatusNoContent && len(resp) != 0 {
			t.Errorf("%s: 204 with body %q", path, resp)
		}
		return code
	}

	// Defaults of a new account.
	u := getUser(t, ts.URL, adminTok, aid)
	c := u.Configuration
	if c.SubtitleMode != "Default" || !c.PlayDefaultAudioTrack || !c.HidePlayedInLatest || c.DisplayMissingEpisodes ||
		c.DisplayCollectionsView || !c.EnableNextEpisodeAutoPlay || !c.RememberAudioSelections || !c.RememberSubtitleSelections || c.EnableLocalPassword {
		t.Errorf("defaults: %+v", c)
	}

	full := `{"SubtitleMode":"OnlyForced","AudioLanguagePreference":"jpn","SubtitleLanguagePreference":"eng","PlayDefaultAudioTrack":false,
		"HidePlayedInLatest":false,"DisplayMissingEpisodes":true,"DisplayCollectionsView":true,"EnableNextEpisodeAutoPlay":false,
		"RememberAudioSelections":false,"RememberSubtitleSelections":false,"EnableLocalPassword":true,
		"OrderedViews":["a"],"GroupedFolders":["g"],"MyMediaExcludes":["m"],"LatestItemsExcludes":["l"],"CastReceiverId":"nope"}`
	if code := post(aliceTok, "/Users/Configuration", full); code != http.StatusNoContent {
		t.Fatalf("own update: %d", code)
	}
	code, body := policyDo(t, ts.URL, aliceTok, http.MethodGet, "/Users/Me", "")
	if code != 200 {
		t.Fatalf("me: %d", code)
	}
	var me userDto
	_ = json.Unmarshal(body, &me)
	c = me.Configuration
	if c.SubtitleMode != "OnlyForced" || derefStr(c.AudioLanguagePreference) != "jpn" || c.SubtitleLanguagePreference != "eng" ||
		c.PlayDefaultAudioTrack || c.HidePlayedInLatest || !c.DisplayMissingEpisodes || !c.DisplayCollectionsView ||
		c.EnableNextEpisodeAutoPlay || c.RememberAudioSelections || c.RememberSubtitleSelections || !c.EnableLocalPassword ||
		len(c.OrderedViews) != 1 || len(c.GroupedFolders) != 1 || len(c.MyMediaExcludes) != 1 || len(c.LatestItemsExcludes) != 1 {
		t.Errorf("after update: %+v", c)
	}
	if c.CastReceiverId != "F007D354" {
		t.Errorf("unknown CastReceiverId not ignored: %q", c.CastReceiverId)
	}

	// Whole replacement.
	if code := post(adminTok, "/Users/Configuration?userId="+aid, `{"SubtitleMode":"None"}`); code != http.StatusNoContent {
		t.Fatalf("replace: %d", code)
	}
	c = getUser(t, ts.URL, adminTok, aid).Configuration
	if c.SubtitleMode != "None" || c.AudioLanguagePreference != nil || c.DisplayMissingEpisodes || !c.PlayDefaultAudioTrack || len(c.OrderedViews) != 0 {
		t.Errorf("not replaced as a whole: %+v", c)
	}

	q := func(id string) string { return "?userId=" + url.QueryEscape(id) }
	for name, tc := range map[string]struct {
		tok, path, body string
		want            int
	}{
		"non-admin other":    {aliceTok, "/Users/Configuration" + q(bid), `{}`, 403},
		"admin other":        {adminTok, "/Users/Configuration" + q(bid), `{"SubtitleMode":"Always"}`, 204},
		"anonymous":          {"", "/Users/Configuration" + q(bid), `{}`, 401},
		"unknown":            {adminTok, "/Users/Configuration" + q(unknownUserID), `{}`, 404},
		"malformed":          {adminTok, "/Users/Configuration", `nope`, 400},
		"no body":            {adminTok, "/Users/Configuration", ``, 400},
		"enum":               {adminTok, "/Users/Configuration", `{"SubtitleMode":"NotAMode"}`, 400},
		"bad id":             {adminTok, "/Users/Configuration?userId=zzz", `{}`, 400},
		"alias self":         {aliceTok, "/Users/" + aid + "/Configuration", `{"SubtitleMode":"Smart"}`, 204},
		"alias non-admin":    {aliceTok, "/Users/" + bid + "/Configuration", `{"SubtitleMode":"Always"}`, 403},
		"alias unknown":      {adminTok, "/Users/" + unknownUserID + "/Configuration", `{}`, 404},
		"alias anonymous":    {"", "/Users/" + bid + "/Configuration", `{}`, 401},
		"alias admin other":  {adminTok, "/Users/" + cid + "/Configuration", `{"SubtitleMode":"None"}`, 204},
		"alias invalid enum": {adminTok, "/Users/" + bid + "/Configuration", `{"SubtitleMode":"x"}`, 400},
	} {
		if code := post(tc.tok, tc.path, tc.body); code != tc.want {
			t.Errorf("%s: status %d, want %d", name, code, tc.want)
		}
	}
	if m := getUser(t, ts.URL, adminTok, aid).Configuration.SubtitleMode; m != "Smart" {
		t.Errorf("alias self: %q", m)
	}
	if m := getUser(t, ts.URL, adminTok, cid).Configuration.SubtitleMode; m != "None" {
		t.Errorf("alias admin: %q", m)
	}
}
