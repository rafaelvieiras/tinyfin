package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// doJSON sends method/path with an optional JSON body and Authorization
// header and returns status, raw body.
func doJSON(t *testing.T, base, method, path, token, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", hdr("wiz-test")+`, Token="`+token+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func expectStatus(t *testing.T, label string, got, want int, body string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: status %d, want %d (body %q)", label, got, want, body)
	}
}

func publicInfo(t *testing.T, base string) publicSystemInfo {
	t.Helper()
	code, body := doJSON(t, base, "GET", "/System/Info/Public", "", "")
	if code != 200 {
		t.Fatalf("public info: %d", code)
	}
	var out publicSystemInfo
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStartupWizardFlow encodes the reference behavior recorded in
// docs/contract/specs/startup.md on a fresh server.
func TestStartupWizardFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	u := ts.URL

	if publicInfo(t, u).StartupWizardCompleted {
		t.Fatal("fresh server reports the wizard as completed")
	}
	if c, b := doJSON(t, u, "GET", "/System/Info", "", ""); c != 200 || !strings.Contains(b, `"StartupWizardCompleted":false`) {
		t.Errorf("anonymous /System/Info on a fresh server: %d %s", c, b)
	}
	if c, b := doJSON(t, u, "GET", "/Users/Public", "", ""); c != 200 || strings.TrimSpace(b) != "[]" {
		t.Errorf("Users/Public: %d %s", c, b)
	}
	for _, p := range []string{"/Users", "/System/Configuration"} {
		c, b := doJSON(t, u, "GET", p, "", "")
		expectStatus(t, "GET "+p, c, 401, b)
	}
	c, b := doJSON(t, u, "POST", "/Users/New", "", `{"Name":"sneaky","Password":"x"}`)
	expectStatus(t, "anonymous Users/New", c, 401, b)

	// Configuration defaults, replace semantics, no validation.
	c, b = doJSON(t, u, "GET", "/Startup/Configuration", "", "")
	if c != 200 || b != `{"ServerName":"","UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"}`+"\n" {
		t.Errorf("default configuration: %d %q", c, b)
	}
	c, b = doJSON(t, u, "POST", "/Startup/Configuration", "bogus-token", `{"ServerName":"Wiz","UICulture":"xx-ZZ"}`)
	expectStatus(t, "POST configuration", c, 204, b)
	c, b = doJSON(t, u, "GET", "/Startup/Configuration", "", "")
	if b != `{"ServerName":"Wiz","UICulture":"xx-ZZ","MetadataCountryCode":"","PreferredMetadataLanguage":""}`+"\n" {
		t.Errorf("replace semantics: %q", b)
	}
	c, b = doJSON(t, u, "POST", "/Startup/Configuration", "", `"notjson"`)
	expectStatus(t, "configuration with a string body", c, 400, b)
	c, b = doJSON(t, u, "POST", "/Startup/Configuration", "", `{"ServerName":"Wizard Test","UICulture":"pt-BR","MetadataCountryCode":"BR","PreferredMetadataLanguage":"pt"}`)
	expectStatus(t, "POST configuration", c, 204, b)
	if got := publicInfo(t, u).ServerName; got != "Wizard Test" {
		t.Errorf("public ServerName = %q", got)
	}
	c, b = doJSON(t, u, "POST", "/Startup/Configuration", "", `{"UICulture":"en-US"}`)
	expectStatus(t, "POST empty server name", c, 204, b)
	if got := publicInfo(t, u).ServerName; got != "TestServer" {
		t.Errorf("empty name must fall back to the default, got %q", got)
	}
	doJSON(t, u, "POST", "/Startup/Configuration", "", `{"ServerName":"Wizard Test","UICulture":"pt-BR","MetadataCountryCode":"BR","PreferredMetadataLanguage":"pt"}`)

	// Methods.
	c, b = doJSON(t, u, "GET", "/Startup/RemoteAccess", "", "")
	expectStatus(t, "GET RemoteAccess", c, 405, b)
	c, b = doJSON(t, u, "POST", "/Startup/FirstUser", "", `{"Name":"a","Password":"b"}`)
	expectStatus(t, "POST FirstUser", c, 405, b)

	// User page: 404 before it is opened.
	c, b = doJSON(t, u, "POST", "/Startup/User", "", `{"Name":"adm","Password":"x"}`)
	expectStatus(t, "POST User before GET", c, 404, b)
	c, b = doJSON(t, u, "GET", "/Startup/User", "", "")
	if c != 200 || strings.TrimSpace(b) != `{"Name":"root"}` {
		t.Errorf("GET User: %d %s", c, b)
	}
	c, b = doJSON(t, u, "GET", "/Startup/FirstUser", "", "")
	if c != 200 || strings.TrimSpace(b) != `{"Name":"root"}` {
		t.Errorf("GET FirstUser: %d %s", c, b)
	}
	// Opening the page created no account (startup.safety.R1).
	if code, _ := loginAs(t, u, "root", "", "wiz-root"); code != 401 {
		t.Errorf("root with an empty password: %d, want 401", code)
	}
	if code, _ := loginAs(t, u, "root", "x", "wiz-root"); code != 401 {
		t.Errorf("root with a password: %d, want 401", code)
	}
	c, b = doJSON(t, u, "POST", "/Startup/Complete", "", "")
	expectStatus(t, "Complete without an account", c, 400, b)
	if publicInfo(t, u).StartupWizardCompleted {
		t.Fatal("Complete without an account completed setup")
	}

	// Validation order and bodies.
	for _, tc := range []struct{ body, text string }{
		{`{"Name":"adm"}`, `"Password must not be empty"`},
		{`{"Name":"adm","Password":null}`, `"Password must not be empty"`},
		{`{"Name":"adm","Password":""}`, `"Password must not be empty"`},
		{`{"Name":"adm","Password":"  "}`, `"Password must not be empty"`},
		{`{"Name":"bad/name"}`, `"Password must not be empty"`},
		{`{}`, `"Password must not be empty"`},
	} {
		c, b := doJSON(t, u, "POST", "/Startup/User", "", tc.body)
		if c != 400 || strings.TrimSpace(b) != tc.text {
			t.Errorf("%s: %d %q", tc.body, c, b)
		}
	}
	for _, name := range []string{"", "   ", "bad/name", "  pad  ", "a<b"} {
		body, _ := json.Marshal(map[string]string{"Name": name, "Password": "x"})
		c, b := doJSON(t, u, "POST", "/Startup/User", "", string(body))
		if c != 400 || b != "Error processing request." {
			t.Errorf("name %q: %d %q", name, c, b)
		}
	}
	c, b = doJSON(t, u, "POST", "/Startup/User", "", `not json`)
	expectStatus(t, "malformed body", c, 400, b)
	if code, _ := loginAs(t, u, "root", "x", "wiz-root"); code != 401 {
		t.Errorf("failed validations created an account: %d", code)
	}

	// Success.
	c, b = doJSON(t, u, "POST", "/Startup/User", "", `{"Name":"Wizard Admin","Password":"Wizard-Pass-1"}`)
	if c != 204 || b != "" {
		t.Fatalf("POST User: %d %q", c, b)
	}
	c, b = doJSON(t, u, "GET", "/Startup/User", "", "")
	if strings.TrimSpace(b) != `{"Name":"Wizard Admin"}` {
		t.Errorf("configured name: %s", b)
	}
	for _, body := range []string{`{"Name":"other","Password":"x"}`, `{}`, `{"Name":"bad/name"}`} {
		c, b = doJSON(t, u, "POST", "/Startup/User", "", body)
		expectStatus(t, "second POST User "+body, c, 403, b)
	}
	if c, b := doJSON(t, u, "GET", "/Users/Public", "", ""); strings.TrimSpace(b) != "[]" {
		t.Errorf("Users/Public after wizard user: %d %s", c, b)
	}
	if publicInfo(t, u).StartupWizardCompleted {
		t.Error("creating the account completed setup")
	}
	code, auth := loginAs(t, u, "Wizard Admin", "Wizard-Pass-1", "wiz-admin")
	if code != 200 {
		t.Fatalf("sign in: %d", code)
	}
	tok := auth.AccessToken
	p := auth.User.Policy
	if !p.IsAdministrator || !p.EnableContentDeletion || !p.EnableRemoteControlOfOtherUsers || !p.IsHidden {
		t.Errorf("wizard account policy: %+v", p)
	}
	c, b = doJSON(t, u, "POST", "/Users/New", tok, `{"Name":"wiz-plain","Password":"Plain-Pass-1"}`)
	expectStatus(t, "administrator creates a user", c, 200, b)
	var plain struct{ Policy userPolicyDto }
	_ = json.Unmarshal([]byte(b), &plain)
	if plain.Policy.EnableContentDeletion || plain.Policy.IsAdministrator {
		t.Errorf("users created later keep the normal defaults: %+v", plain.Policy)
	}

	// Remote access.
	c, b = doJSON(t, u, "POST", "/Startup/RemoteAccess", "", `{"EnableRemoteAccess":"x"}`)
	expectStatus(t, "RemoteAccess wrong type", c, 400, b)
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"EnableRemoteAccess":false,"EnableAutomaticPortMapping":true}`, false},
		{`{"EnableRemoteAccess":true,"EnableAutomaticPortMapping":true}`, true},
		{`{}`, false},
	} {
		c, b = doJSON(t, u, "POST", "/Startup/RemoteAccess", "", tc.body)
		expectStatus(t, "RemoteAccess "+tc.body, c, 204, b)
		_, nb := doJSON(t, u, "GET", "/System/Configuration/network", tok, "")
		var net networkConfigurationDto
		_ = json.Unmarshal([]byte(nb), &net)
		if net.EnableRemoteAccess != tc.want || net.EnableUPnP {
			t.Errorf("after %s: %+v", tc.body, net)
		}
	}
	_, cb := doJSON(t, u, "GET", "/System/Configuration", tok, "")
	var sc serverConfigurationDto
	_ = json.Unmarshal([]byte(cb), &sc)
	if sc.ServerName != "Wizard Test" || sc.UICulture != "pt-BR" || sc.MetadataCountryCode != "BR" || sc.PreferredMetadataLanguage != "pt" {
		t.Errorf("System/Configuration: %+v", sc)
	}

	// Complete.
	c, b = doJSON(t, u, "POST", "/Startup/Complete", "", "")
	expectStatus(t, "Complete", c, 204, b)
	if !publicInfo(t, u).StartupWizardCompleted {
		t.Error("Complete did not set the flag")
	}
	c, b = doJSON(t, u, "GET", "/System/Info", "", "")
	expectStatus(t, "anonymous /System/Info after setup", c, 401, b)

	// Anonymous and unknown-token callers are refused on every route.
	for _, r := range [][3]string{
		{"GET", "/Startup/Configuration", ""}, {"GET", "/Startup/User", ""}, {"GET", "/Startup/FirstUser", ""},
		{"POST", "/Startup/Configuration", `{"ServerName":"Hacked"}`}, {"POST", "/Startup/User", `{"Name":"hacker","Password":"x"}`},
		{"POST", "/Startup/RemoteAccess", `{"EnableRemoteAccess":true}`}, {"POST", "/Startup/Complete", ""},
	} {
		c, b = doJSON(t, u, r[0], r[1], "", r[2])
		expectStatus(t, "anonymous "+r[0]+" "+r[1], c, 401, b)
		c, b = doJSON(t, u, r[0], r[1], "bogus", r[2])
		expectStatus(t, "unknown token "+r[0]+" "+r[1], c, 401, b)
	}
	if got := publicInfo(t, u).ServerName; got != "Wizard Test" {
		t.Errorf("refused calls changed the name: %q", got)
	}

	// Administrator: reads and the idempotent posts work, the account does not.
	for _, r := range [][4]string{
		{"GET", "/Startup/Configuration", "", "200"}, {"GET", "/Startup/User", "", "200"}, {"GET", "/Startup/FirstUser", "", "200"},
		{"POST", "/Startup/Configuration", `{"ServerName":"Wizard Test","UICulture":"pt-BR","MetadataCountryCode":"BR","PreferredMetadataLanguage":"pt"}`, "204"},
		{"POST", "/Startup/RemoteAccess", `{"EnableRemoteAccess":true}`, "204"},
		{"POST", "/Startup/Complete", "", "204"},
		{"POST", "/Startup/User", `{"Name":"hacker","Password":"x"}`, "403"},
	} {
		c, b = doJSON(t, u, r[0], r[1], tok, r[2])
		if want := map[string]int{"200": 200, "204": 204, "403": 403}[r[3]]; c != want {
			t.Errorf("administrator %s %s: %d, want %d (%s)", r[0], r[1], c, want, b)
		}
	}
	// Non-administrator: 403 everywhere.
	code, plainAuth := loginAs(t, u, "wiz-plain", "Plain-Pass-1", "wiz-plain")
	if code != 200 {
		t.Fatalf("plain sign in: %d", code)
	}
	for _, r := range [][3]string{
		{"GET", "/Startup/Configuration", ""}, {"GET", "/Startup/User", ""}, {"POST", "/Startup/Configuration", `{"ServerName":"Hacked"}`},
		{"POST", "/Startup/User", `{"Name":"hacker","Password":"x"}`}, {"POST", "/Startup/RemoteAccess", `{}`}, {"POST", "/Startup/Complete", ""},
	} {
		c, b = doJSON(t, u, r[0], r[1], plainAuth.AccessToken, r[2])
		expectStatus(t, "non-administrator "+r[0]+" "+r[1], c, 403, b)
	}
}

// TestStartupWizardPersistsAcrossRestart: the wizard's progress and its
// completion are persisted, a restart neither re-opens nor completes it.
func TestStartupWizardPersistsAcrossRestart(t *testing.T) {
	ts, st := newTestServer(t)
	u := ts.URL
	doJSON(t, u, "GET", "/Startup/User", "", "")
	if c, b := doJSON(t, u, "POST", "/Startup/User", "", `{"Name":"adm","Password":"pw"}`); c != 204 {
		t.Fatalf("POST User: %d %s", c, b)
	}
	// A restart before Complete keeps the wizard open and its account.
	ts2 := httptest.NewServer(NewServer(st, Config{ServerName: "TestServer"}).Handler())
	defer ts2.Close()
	if publicInfo(t, ts2.URL).StartupWizardCompleted {
		t.Fatal("wizard completed by a restart")
	}
	if c, b := doJSON(t, ts2.URL, "POST", "/Startup/User", "", `{"Name":"x","Password":"pw"}`); c != 403 {
		t.Errorf("second account after restart: %d %s", c, b)
	}
	if c, b := doJSON(t, ts2.URL, "POST", "/Startup/Complete", "", ""); c != 204 {
		t.Fatalf("Complete: %d %s", c, b)
	}
	ts3 := httptest.NewServer(NewServer(st, Config{ServerName: "TestServer"}).Handler())
	defer ts3.Close()
	if !publicInfo(t, ts3.URL).StartupWizardCompleted {
		t.Error("completion not persisted")
	}
	if c, _ := doJSON(t, ts3.URL, "GET", "/Startup/Configuration", "", ""); c != 401 {
		t.Errorf("anonymous GET after restart: %d", c)
	}
}

// startup.complete.R3: accounts that predate the wizard (or were seeded out
// of band) mean "set up", and that verdict survives deleting them.
func TestStartupExistingAccountsMeanSetUp(t *testing.T) {
	ts, st := newTestServer(t)
	u := ts.URL
	if publicInfo(t, u).StartupWizardCompleted {
		t.Fatal("fresh server already complete")
	}
	user := createTestAdminUser(t, st, "seeded", "pw")
	if !publicInfo(t, u).StartupWizardCompleted {
		t.Fatal("a server with accounts must report the wizard as completed")
	}
	// The anonymous routes are closed, with and without the in-memory latch.
	fresh := httptest.NewServer(NewServer(st, Config{ServerName: "TestServer"}).Handler())
	defer fresh.Close()
	for _, base := range []string{u, fresh.URL} {
		if c, b := doJSON(t, base, "POST", "/Startup/User", "", `{"Name":"evil","Password":"x"}`); c != 401 {
			t.Errorf("anonymous POST User on a seeded server: %d %s", c, b)
		}
		if c, b := doJSON(t, base, "GET", "/Startup/Configuration", "", ""); c != 401 {
			t.Errorf("anonymous GET configuration on a seeded server: %d %s", c, b)
		}
	}
	// Removing every account must not re-open setup.
	if err := st.DeleteUser(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	again := httptest.NewServer(NewServer(st, Config{ServerName: "TestServer"}).Handler())
	defer again.Close()
	if c, b := doJSON(t, again.URL, "POST", "/Startup/User", "", `{"Name":"evil","Password":"x"}`); c != 401 {
		t.Errorf("setup re-opened after deleting the accounts: %d %s", c, b)
	}
	if v, ok, _ := st.GetSetting(context.Background(), storage.SettingStartupWizardCompleted); !ok || v != "true" {
		t.Errorf("verdict not persisted: %q %v", v, ok)
	}
}

// An API key authenticates as an administrator on the closed routes
// (startup.access.R4).
func TestStartupRoutesAcceptAPIKeyAfterSetUp(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "pw")
	key, err := st.CreateAPIKey(context.Background(), "wiz")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{
		{"GET", "/Startup/Configuration", "200"}, {"POST", "/Startup/Complete", "204"}, {"POST", "/Startup/User", "403"},
	} {
		c, b := doJSON(t, ts.URL, r[0], r[1]+"?ApiKey="+key.Token, "", `{"Name":"x","Password":"y"}`)
		want := map[string]int{"200": 200, "204": 204, "403": 403}[r[2]]
		if r[0] == "GET" {
			c, b = doJSON(t, ts.URL, "GET", r[1]+"?ApiKey="+key.Token, "", "")
		}
		if c != want {
			t.Errorf("API key %s %s: %d, want %d (%s)", r[0], r[1], c, want, b)
		}
	}
}
