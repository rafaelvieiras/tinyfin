package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newTestServer opens a fresh SQLite store on a t.TempDir() file and wraps
// it in a running httptest.Server, returning both — most tests need the
// store directly (to seed a user) as well as the HTTP server.
func newTestServer(t *testing.T) (*httptest.Server, *sqlite.Store) {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	srv := NewServer(st, Config{ServerName: "TestServer"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// createTestUser inserts a user directly through the storage layer (not
// through the HTTP API) with a bcrypt-hashed password, mirroring how a real
// deployment's password would be stored.
func createTestUser(t *testing.T, st storage.UserStore, name, password string) storage.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	id, err := st.CreateUser(context.Background(), storage.User{Name: name, PasswordHash: string(hash)})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := st.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	return u
}

// createTestAdminUser is createTestUser plus IsAdministrator: true — for
// tests covering baseItemDto's admin-gated fields (CanDelete/CanDownload,
// docs/12's "Episode DTO — item-level video/permission/identity fields").
func createTestAdminUser(t *testing.T, st storage.UserStore, name, password string) storage.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	id, err := st.CreateUser(context.Background(), storage.User{Name: name, PasswordHash: string(hash), IsAdministrator: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := st.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	return u
}

func TestSystemInfoPublicNoAuth(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/System/Info/Public")
	if err != nil {
		t.Fatalf("GET /System/Info/Public: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got publicSystemInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ServerName != "TestServer" {
		t.Errorf("ServerName = %q, want %q", got.ServerName, "TestServer")
	}
	if got.Version != Version {
		t.Errorf("Version = %q, want %q", got.Version, Version)
	}
}

func authenticate(t *testing.T, baseURL, username, password string) *http.Response {
	t.Helper()
	reqBody, err := json.Marshal(authenticateUserByName{Username: username, Pw: password})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/Users/AuthenticateByName", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="TestClient", Device="TestDevice", DeviceId="test-device-1", Version="1.0.0"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /Users/AuthenticateByName: %v", err)
	}
	return resp
}

func TestAuthenticateByNameAndUsersMe(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "alice", "hunter2")

	resp := authenticate(t, ts.URL, user.Name, "hunter2")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var authResult authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&authResult); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}
	if authResult.AccessToken == "" {
		t.Fatal("AccessToken is empty")
	}
	if authResult.User.Name != "alice" {
		t.Errorf("User.Name = %q, want %q", authResult.User.Name, "alice")
	}

	// /Users/Me with the token succeeds.
	meReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/Users/Me", nil)
	meReq.Header.Set("X-Emby-Token", authResult.AccessToken)
	meResp, err := http.DefaultClient.Do(meReq)
	if err != nil {
		t.Fatalf("GET /Users/Me: %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", meResp.StatusCode)
	}
	var me userDto
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decoding /Users/Me response: %v", err)
	}
	if me.Name != "alice" {
		t.Errorf("Name = %q, want %q", me.Name, "alice")
	}
	if !me.HasConfiguredPassword {
		t.Error("HasConfiguredPassword = false, want true")
	}

	// /Users/Me with no token at all fails with 401.
	noAuthResp, err := http.Get(ts.URL + "/Users/Me")
	if err != nil {
		t.Fatalf("GET /Users/Me (no auth): %v", err)
	}
	defer noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", noAuthResp.StatusCode)
	}

	// /Users/Me with a garbage token also fails with 401, not a crash.
	badReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/Users/Me", nil)
	badReq.Header.Set("X-Emby-Token", "not-a-real-token")
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("GET /Users/Me (bad token): %v", err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", badResp.StatusCode)
	}
}

func TestAuthenticateByNameWrongPassword(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "bob", "correct-password")

	resp := authenticate(t, ts.URL, user.Name, "wrong-password")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAuthenticateByNameSuccessAppendsActivityLogEntry regression-guards
// handleAuthenticateByName's success path (auth_by_name.go's
// logAuthSuccess): a SessionStarted entry, UserID set to the logged-in
// user, Severity "Information".
func TestAuthenticateByNameSuccessAppendsActivityLogEntry(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "erin", "hunter2")

	resp := authenticate(t, ts.URL, user.Name, "hunter2")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	hasUser := true
	entries, total, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 10, HasUserID: &hasUser})
	if err != nil {
		t.Fatalf("QueryActivityEntries: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("total=%d len(entries)=%d, want exactly one hasUserId=true entry", total, len(entries))
	}
	e := entries[0]
	if e.Type != storage.ActivityTypeSessionStarted {
		t.Errorf("Type = %q, want %q", e.Type, storage.ActivityTypeSessionStarted)
	}
	if e.UserID == nil || *e.UserID != user.ID {
		t.Errorf("UserID = %v, want %q", e.UserID, user.ID)
	}
	if e.Severity != storage.ActivitySeverityInfo {
		t.Errorf("Severity = %q, want %q", e.Severity, storage.ActivitySeverityInfo)
	}
}

// TestAuthenticateByNameFailureAppendsActivityLogEntry regression-guards
// handleAuthenticateByName's two failure paths (auth_by_name.go's
// logAuthFailure, called for both "no such user" and "wrong password"): an
// AuthenticationFailed entry with no UserID, Severity "Error", distinct
// from what a successful login appends.
func TestAuthenticateByNameFailureAppendsActivityLogEntry(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "frank", "correct-password")

	resp := authenticate(t, ts.URL, user.Name, "wrong-password")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	noUser := false
	entries, total, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 10, HasUserID: &noUser})
	if err != nil {
		t.Fatalf("QueryActivityEntries: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("total=%d len(entries)=%d, want exactly one hasUserId=false entry", total, len(entries))
	}
	e := entries[0]
	if e.Type != storage.ActivityTypeAuthenticationFailed {
		t.Errorf("Type = %q, want %q", e.Type, storage.ActivityTypeAuthenticationFailed)
	}
	if e.UserID != nil {
		t.Errorf("UserID = %v, want nil", *e.UserID)
	}
	if e.Severity != storage.ActivitySeverityError {
		t.Errorf("Severity = %q, want %q", e.Severity, storage.ActivitySeverityError)
	}

	// A login against a wholly unknown username hits the other failure
	// call site (GetUserByName error) and appends the same shape of entry.
	resp2 := authenticate(t, ts.URL, "no-such-user", "whatever")
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp2.StatusCode)
	}
	_, total2, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 10, HasUserID: &noUser})
	if err != nil {
		t.Fatalf("QueryActivityEntries: %v", err)
	}
	if total2 != 2 {
		t.Fatalf("total2 = %d, want 2 (both failure call sites logged)", total2)
	}
}

func TestUsersListCreateDelete(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestUser(t, st, "admin", "adminpass")

	resp := authenticate(t, ts.URL, admin.Name, "adminpass")
	defer resp.Body.Close()
	var authResult authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&authResult); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}

	authedReq := func(method, path string, body []byte) *http.Response {
		t.Helper()
		var r *http.Request
		var err error
		if body != nil {
			r, err = http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r, err = http.NewRequest(method, ts.URL+path, nil)
		}
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		r.Header.Set("X-Emby-Token", authResult.AccessToken)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// Create a second user via POST /Users/New.
	createBody, _ := json.Marshal(createUserByName{Name: "carol", Password: "carolpass"})
	createResp := authedReq(http.MethodPost, "/Users/New", createBody)
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /Users/New status = %d, want 200", createResp.StatusCode)
	}
	var created userDto
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding created user: %v", err)
	}
	if created.Name != "carol" {
		t.Errorf("created.Name = %q, want %q", created.Name, "carol")
	}

	// List includes both users.
	listResp := authedReq(http.MethodGet, "/Users", nil)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Users status = %d, want 200", listResp.StatusCode)
	}
	var users []userDto
	if err := json.NewDecoder(listResp.Body).Decode(&users); err != nil {
		t.Fatalf("decoding user list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, want 2", len(users))
	}

	// Delete the newly created user.
	delResp := authedReq(http.MethodDelete, "/Users/"+created.ID, nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /Users/{id} status = %d, want 204", delResp.StatusCode)
	}

	listResp2 := authedReq(http.MethodGet, "/Users", nil)
	defer listResp2.Body.Close()
	var usersAfter []userDto
	if err := json.NewDecoder(listResp2.Body).Decode(&usersAfter); err != nil {
		t.Fatalf("decoding user list: %v", err)
	}
	if len(usersAfter) != 1 {
		t.Fatalf("len(usersAfter) = %d, want 1", len(usersAfter))
	}
}
