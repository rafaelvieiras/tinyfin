package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// TestCanonicalDeviceID covers the legacy per-user DeviceId an official
// client keeps after a sign-in (deviceid.go, devices.R11).
func TestCanonicalDeviceID(t *testing.T) {
	const userID = "18de42d3c403f61794659f94fe1a3022"
	const hyphenated = "18de42d3-c403-f617-9465-9f94fe1a3022"
	cases := []struct{ name, deviceID, want string }{
		{"hyphenated suffix of the caller", "573c553d8a24e29f" + hyphenated, "573c553d8a24e29f"},
		{"bare suffix of the caller", "573c553d8a24e29f" + userID, "573c553d8a24e29f"},
		{"uppercase suffix", "573c553d8a24e29f" + "18DE42D3-C403-F617-9465-9F94FE1A3022", "573c553d8a24e29f"},
		{"plain id", "573c553d8a24e29f", "573c553d8a24e29f"},
		{"another account's suffix is a real id", "573c553d8a24e29f18d15755-467d-e6c4-39fa-c8bd50274d6e", "573c553d8a24e29f18d15755-467d-e6c4-39fa-c8bd50274d6e"},
		{"the account id alone is the whole id", hyphenated, hyphenated},
		{"no account to strip", "573c553d8a24e29f" + hyphenated, "573c553d8a24e29f"},
	}
	for _, c := range cases {
		if got := canonicalDeviceID(c.deviceID, userID); got != c.want {
			t.Errorf("%s: canonicalDeviceID(%q) = %q, want %q", c.name, c.deviceID, got, c.want)
		}
	}
	if got := canonicalDeviceID("any", ""); got != "any" {
		t.Errorf("no user: got %q", got)
	}
	if got := canonicalDeviceID("", userID); got != "" {
		t.Errorf("no device id: got %q", got)
	}
}

// TestSessionsSurviveRestart proves the registry is written through to the
// store and read back at startup (devices.R13): a second server built on the
// same database lists the session the first one registered, keeps its stable
// session id, and reports no now-playing state for it.
func TestSessionsSurviveRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tinyfin.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	user := createTestAdminUser(t, st, "alice", "hunter2")

	srv := NewServer(st, Config{ServerName: "TestServer"})
	ts := httptest.NewServer(srv.Handler())
	// The login carries the same DeviceId as the rest of the test: that is
	// what registers the device entry DELETE /Devices acts on.
	loginResp := sessionsAuthedRequest(t, ts.URL, "", "Jellyfin Web", "Chrome", "device-restart", "10.11.11", http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": user.Name, "Pw": "hunter2"})
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResp.StatusCode)
	}
	var login struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatalf("decoding login: %v", err)
	}
	_ = loginResp.Body.Close()
	token := login.AccessToken

	caps := sessionCapabilitiesDto{PlayableMediaTypes: []string{"Video"}, SupportsMediaControl: true}
	if resp := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin Web", "Chrome", "device-restart", "10.11.11", http.MethodPost, "/Sessions/Capabilities/Full", caps); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("capabilities status = %d", resp.StatusCode)
	}
	before := listSessions(t, ts.URL, token)
	if len(before) != 1 {
		t.Fatalf("before restart: %d sessions, want 1", len(before))
	}
	ts.Close()
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A restart: same database file, brand new store and server, nothing
	// reported to the second one yet.
	st2, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("sqlite.New (second): %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	srv2 := NewServer(st2, Config{ServerName: "TestServer"})
	ts2 := httptest.NewServer(srv2.Handler())
	t.Cleanup(ts2.Close)

	after := listSessions(t, ts2.URL, token)
	if len(after) != 1 {
		t.Fatalf("after restart: %d sessions, want 1", len(after))
	}
	got := after[0]
	if got.DeviceID != "device-restart" || got.Client != "Jellyfin Web" || got.DeviceName != "Chrome" {
		t.Errorf("restored identity = %+v", got)
	}
	if got.ID != before[0].ID {
		t.Errorf("session id changed across restart: %q -> %q", before[0].ID, got.ID)
	}
	if len(got.Capabilities.PlayableMediaTypes) != 1 || got.Capabilities.PlayableMediaTypes[0] != "Video" {
		t.Errorf("capabilities not restored: %+v", got.Capabilities)
	}
	if got.NowPlayingItem != nil {
		t.Errorf("now-playing state must not survive a restart: %+v", got.NowPlayingItem)
	}

	// Deleting the device must drop the persisted session too, or the next
	// restart would resurrect it.
	if resp := sessionsAuthedRequest(t, ts2.URL, token, "Jellyfin Web", "Chrome", "device-restart", "10.11.11", http.MethodDelete, "/Devices?id=device-restart", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /Devices status = %d", resp.StatusCode)
	}
	// Deleting the device revoked its token (devices.R8), so the check
	// signs in again; the new login must find no session to list.
	relogin := sessionsAuthedRequest(t, ts2.URL, "", "Jellyfin Web", "Chrome", "device-restart", "10.11.11", http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": user.Name, "Pw": "hunter2"})
	if relogin.StatusCode != http.StatusOK {
		t.Fatalf("re-login status = %d", relogin.StatusCode)
	}
	var again struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.NewDecoder(relogin.Body).Decode(&again); err != nil {
		t.Fatalf("decoding re-login: %v", err)
	}
	_ = relogin.Body.Close()
	if left := listSessions(t, ts2.URL, again.AccessToken); len(left) != 0 {
		t.Fatalf("after device deletion: %d sessions, want 0", len(left))
	}
	if saved, err := st2.QuerySessions(context.Background()); err != nil || len(saved) != 0 {
		t.Fatalf("persisted sessions after deletion = %v, %v", saved, err)
	}
}

// listSessions reads GET /Sessions as the given token.
func listSessions(t *testing.T, tsURL, token string) []sessionInfoDto {
	t.Helper()
	resp := sessionsAuthedRequest(t, tsURL, token, "Jellyfin Web", "Chrome", "device-restart", "10.11.11", http.MethodGet, "/Sessions", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Sessions status = %d, want 200", resp.StatusCode)
	}
	var out []sessionInfoDto
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding /Sessions: %v", err)
	}
	return out
}
