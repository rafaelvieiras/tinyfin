package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sessionsAuthedRequest issues a request carrying the full MediaBrowser
// client-identification header (Client/Device/DeviceId/Version) plus Token
// — unlike authedClient/adminClient (which send only Token), this is
// what a POST /Sessions/Capabilities/Full or GET /Sessions caller needs
// here: the session registry keys off AuthContext.DeviceID, which is
// populated only when the Authorization header carries a DeviceId field.
func sessionsAuthedRequest(t *testing.T, tsURL, token, client, device, deviceID, version, method, path string, body any) *http.Response {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		b, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			t.Fatalf("marshaling body: %v", marshalErr)
		}
		r, err = http.NewRequest(method, tsURL+path, bytes.NewReader(b))
		if err == nil {
			r.Header.Set("Content-Type", "application/json")
		}
	} else {
		r, err = http.NewRequest(method, tsURL+path, nil)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Authorization", `MediaBrowser Client="`+client+`", Device="`+device+`", DeviceId="`+deviceID+`", Version="`+version+`", Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// TestSessionsCapabilitiesFullRegistersSession covers the end-to-end path:
// POST /Sessions/Capabilities/Full (system.go's handleSessionsCapabilitiesFull)
// upserts into the registry, and a subsequent GET /Sessions sees it with the
// fields ground-truth-confirmed against a real Jellyfin server.
func TestSessionsCapabilitiesFullRegistersSession(t *testing.T) {
	ts, st := newTestServer(t)
	// GET /Sessions is admin-only (server.go) — it lists every user's
	// session, IP address included, not just the caller's own.
	user := createTestAdminUser(t, st, "alice", "hunter2")
	// The login registers the session (users.auth.R33) and the capabilities
	// below fill that same session, so the login carries the device id the
	// rest of the test uses.
	token := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "device-alice-1")

	capBody := sessionCapabilitiesDto{
		PlayableMediaTypes:           []string{"Audio", "Video"},
		SupportedCommands:            []string{"MoveUp", "MoveDown"},
		SupportsPersistentIdentifier: false,
		SupportsMediaControl:         true,
	}
	capResp := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin Web", "Chrome", "device-alice-1", "10.11.11", http.MethodPost, "/Sessions/Capabilities/Full", capBody)
	defer capResp.Body.Close()
	if capResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /Sessions/Capabilities/Full status = %d, want 204", capResp.StatusCode)
	}

	listResp := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin Web", "Chrome", "device-alice-1", "10.11.11", http.MethodGet, "/Sessions", nil)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Sessions status = %d, want 200", listResp.StatusCode)
	}
	var sessions []sessionInfoDto
	if err := json.NewDecoder(listResp.Body).Decode(&sessions); err != nil {
		t.Fatalf("decoding /Sessions response: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}

	got := sessions[0]
	if got.DeviceID != "device-alice-1" {
		t.Errorf("DeviceId = %q, want %q", got.DeviceID, "device-alice-1")
	}
	if got.UserID != string(user.ID) {
		t.Errorf("UserId = %q, want %q", got.UserID, string(user.ID))
	}
	if got.UserName != "alice" {
		t.Errorf("UserName = %q, want %q", got.UserName, "alice")
	}
	if got.Client != "Jellyfin Web" {
		t.Errorf("Client = %q, want %q", got.Client, "Jellyfin Web")
	}
	if got.DeviceName != "Chrome" {
		t.Errorf("DeviceName = %q, want %q", got.DeviceName, "Chrome")
	}
	if got.ApplicationVersion != "10.11.11" {
		t.Errorf("ApplicationVersion = %q, want %q", got.ApplicationVersion, "10.11.11")
	}
	if !got.Capabilities.SupportsMediaControl || !got.SupportsMediaControl || !got.SupportsRemoteControl {
		t.Errorf("SupportsMediaControl/SupportsRemoteControl not all true: %+v", got)
	}
	if len(got.SupportedCommands) != 2 {
		t.Errorf("SupportedCommands = %+v, want 2 entries", got.SupportedCommands)
	}
	if len(got.AdditionalUsers) != 0 {
		t.Errorf("AdditionalUsers = %+v, want empty", got.AdditionalUsers)
	}
	if len(got.NowPlayingQueue) != 0 {
		t.Errorf("NowPlayingQueue not empty: %+v", got.NowPlayingQueue)
	}
	if !got.LastPlaybackCheckIn.IsZero() {
		t.Errorf("LastPlaybackCheckIn = %v, want zero value", got.LastPlaybackCheckIn)
	}
	if !got.IsActive {
		t.Error("IsActive = false, want true")
	}
	if got.ID == "" {
		t.Error("Id is empty, want a minted session id")
	}
}

// TestSessionsActiveWithinSecondsExcludesStale covers GET /Sessions's
// optional activeWithinSeconds filter: a session touched two hours ago is
// excluded when the window is 60 seconds, and included again when no filter
// is given at all (confirming "absent means unfiltered", not a hidden
// default window).
func TestSessionsActiveWithinSecondsExcludesStale(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	srv.sessions.upsertCapabilities(context.Background(), "dev-fresh", "user-1", "Fresh", "Jellyfin Web", "Chrome", "10.11.11", "10.0.0.5", sessionCapabilitiesDto{SupportsMediaControl: true})
	srv.sessions.upsertCapabilities(context.Background(), "dev-stale", "user-2", "Stale", "Jellyfin Web", "Chrome", "10.11.11", "10.0.0.6", sessionCapabilitiesDto{SupportsMediaControl: true})

	// Directly age the second entry's LastActivityDate well past the filter
	// window — same-package access to sessionRegistry's unexported fields,
	// simpler here than injecting a fake clock for one test.
	srv.sessions.byDevice["dev-stale"].lastActivityDate = time.Now().UTC().Add(-2 * time.Hour)

	w := callHandlerDirect(t, srv.handleSessionsList, http.MethodGet, "/Sessions?activeWithinSeconds=60", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var sessions []sessionInfoDto
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1 (stale entry should be excluded)", len(sessions))
	}
	if sessions[0].DeviceID != "dev-fresh" {
		t.Errorf("DeviceId = %q, want %q", sessions[0].DeviceID, "dev-fresh")
	}

	wAll := callHandlerDirect(t, srv.handleSessionsList, http.MethodGet, "/Sessions", nil)
	if wAll.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", wAll.Code)
	}
	var allSessions []sessionInfoDto
	if err := json.Unmarshal(wAll.Body.Bytes(), &allSessions); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(allSessions) != 2 {
		t.Fatalf("len(allSessions) = %d, want 2 (no activeWithinSeconds means unfiltered)", len(allSessions))
	}
}

// TestSessionsListEmptyIsEmptyArrayNotNull covers the "no tracked sessions
// yet" case: the response must serialize to "[]", not "null" — a client
// deserializing GET /Sessions into a fixed-shape list expects an array
// either way.
func TestSessionsListEmptyIsEmptyArrayNotNull(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleSessionsList, http.MethodGet, "/Sessions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want %q", got, "[]")
	}
}

// TestLoginListsTheSessionAndLogoutDropsIt covers users.auth.R33 and
// users.token.R10: the reference lists a device in GET /Sessions as soon as
// it logs in — before it reports any capabilities — and drops it when the
// session ends. Measured against the reference: a login answers 200 and
// GET /Sessions then holds one entry for that DeviceId; POST /Sessions/Logout
// answers 204 and the entry is gone.
func TestLoginListsTheSessionAndLogoutDropsIt(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestAdminUser(t, st, "alice", "hunter2")
	token := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "device-alice-9")

	listSessions := func() []sessionInfoDto {
		t.Helper()
		resp := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin Web", "Chrome", "device-alice-9", "10.11.11", http.MethodGet, "/Sessions", nil)
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

	sessions := listSessions()
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) after login = %d, want 1", len(sessions))
	}
	if sessions[0].DeviceID != "device-alice-9" {
		t.Errorf("DeviceId = %q, want device-alice-9", sessions[0].DeviceID)
	}
	if sessions[0].UserID != string(user.ID) {
		t.Errorf("UserId = %q, want %q", sessions[0].UserID, user.ID)
	}
	if sessions[0].LastActivityDate.IsZero() {
		t.Error("LastActivityDate is zero, want the login time")
	}
	if !sessions[0].LastPlaybackCheckIn.IsZero() {
		t.Errorf("LastPlaybackCheckIn = %v, want the zero date (wire.date.R3)", sessions[0].LastPlaybackCheckIn)
	}

	logout := sessionsAuthedRequest(t, ts.URL, token, "Jellyfin Web", "Chrome", "device-alice-9", "10.11.11", http.MethodPost, "/Sessions/Logout", nil)
	defer logout.Body.Close()
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /Sessions/Logout status = %d, want 204", logout.StatusCode)
	}
	// The token is gone, so list through a fresh login on another device.
	other := loginTokenWithDevice(t, ts.URL, user.Name, "hunter2", "device-alice-10")
	_ = other
	resp := sessionsAuthedRequest(t, ts.URL, other, "Jellyfin Web", "Chrome", "device-alice-10", "10.11.11", http.MethodGet, "/Sessions", nil)
	defer resp.Body.Close()
	var after []sessionInfoDto
	if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
		t.Fatalf("decoding /Sessions: %v", err)
	}
	for _, s := range after {
		if s.DeviceID == "device-alice-9" {
			t.Fatalf("the logged-out device is still listed: %#v", s)
		}
	}
}
