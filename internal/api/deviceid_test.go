package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestRequestDeviceIDSuffixIsCollapsed covers devices.R11 on the wire: a
// request whose DeviceId carries the caller's own account id lands on the
// device entry of the bare id, so one install cannot create a second entry.
func TestRequestDeviceIDSuffixIsCollapsed(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestAdminUser(t, st, "alice", "hunter2")
	token := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "dev-raw")

	legacy := "dev-raw" + hyphenatedUserID(string(user.ID))
	if resp := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin for Android", "Redmi", legacy, "2.7.3", http.MethodGet, "/Users/Me", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("request with legacy DeviceId status = %d, want 200", resp.StatusCode)
	}
	devices, err := st.QueryDevices(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("QueryDevices: %v", err)
	}
	if len(devices) != 1 || string(devices[0].ID) != "dev-raw" {
		t.Fatalf("devices = %+v, want exactly one entry for dev-raw", devices)
	}
}

// TestLoginCollapsesLegacyDeviceEntry covers devices.R12: an install that a
// previous sign-in left stored under the account-suffixed DeviceId collapses
// into the bare one on the next login, entry and token alike.
func TestLoginCollapsesLegacyDeviceEntry(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestAdminUser(t, st, "alice", "hunter2")
	ctx := context.Background()

	// The state an upgraded install has: a device entry and a live token
	// under the suffixed id.
	legacy := storage.DeviceID("dev-raw" + hyphenatedUserID(string(user.ID)))
	if err := st.UpsertDevice(ctx, storage.Device{
		ID: legacy, Name: "Redmi", AppName: "Jellyfin for Android", AppVersion: "2.7.3",
		LastUserID: user.ID, LastUserName: user.Name, DateLastActivity: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	legacyToken, err := st.IssueToken(ctx, user.ID, legacy)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "dev-raw")

	devices, err := st.QueryDevices(ctx, user.ID)
	if err != nil {
		t.Fatalf("QueryDevices: %v", err)
	}
	if len(devices) != 1 || string(devices[0].ID) != "dev-raw" {
		t.Fatalf("devices after re-login = %+v, want only dev-raw", devices)
	}
	if resp := sessionsAuthedRequest(t, ts.URL, legacyToken.Token, "Jellyfin for Android", "Redmi", string(legacy), "2.7.3", http.MethodGet, "/Users/Me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("legacy token status = %d, want 401 (revoked with the entry)", resp.StatusCode)
	}
}

// hyphenatedUserID formats a 32-character user id as the hyphenated UUID the
// clients put on the wire.
func hyphenatedUserID(userID string) string {
	return userID[:8] + "-" + userID[8:12] + "-" + userID[12:16] + "-" + userID[16:20] + "-" + userID[20:]
}

// loginTokenWithDevice signs in carrying a chosen DeviceId and returns the
// token.
func loginTokenWithDevice(t *testing.T, tsURL, username, password, deviceID string) string {
	t.Helper()
	resp := sessionsAuthedRequest(t, tsURL, "", "Jellyfin Web", "Chrome", deviceID, "10.11.11", http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": username, "Pw": password})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding login: %v", err)
	}
	return out.AccessToken
}

// TestLoginWithSuffixedDeviceIDUsesTheBareOne covers devices.R11 at the
// login itself: the token is issued to the bare device, so signing in again
// with the account id appended is a sign-in of the same device and replaces
// its token instead of adding one (devices.R3).
func TestLoginWithSuffixedDeviceIDUsesTheBareOne(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestAdminUser(t, st, "alice", "hunter2")

	first := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "dev-raw")
	second := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "dev-raw"+hyphenatedUserID(string(user.ID)))
	if first == second {
		t.Fatalf("the second login reissued the same token")
	}

	tokens, err := st.QueryUserTokens(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("QueryUserTokens: %v", err)
	}
	if len(tokens) != 1 || string(tokens[0].DeviceID) != "dev-raw" {
		t.Fatalf("tokens = %+v, want one token for dev-raw", tokens)
	}
	if resp := sessionsAuthedRequest(t, ts.URL, first, "Jellyfin Web", "Chrome", "dev-raw", "10.11.11", http.MethodGet, "/Users/Me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first token status = %d, want 401 (replaced)", resp.StatusCode)
	}
	if resp := sessionsAuthedRequest(t, ts.URL, second, "Jellyfin Web", "Chrome", "dev-raw", "10.11.11", http.MethodGet, "/Users/Me", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("second token status = %d, want 200", resp.StatusCode)
	}
}
