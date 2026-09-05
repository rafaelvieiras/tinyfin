package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newTestServerWithActivityLog is newTestServer (api_test.go) plus a
// locally built mux exposing GET /System/ActivityLog/Entries —
// deliberately not registered in server.go yet (route wiring is a
// separate, coordinated change touching a file this task must not edit,
// same reasoning as scheduled_tasks_test.go's
// newTestServerWithScheduledTasks and item_images_admin_test.go's
// newTestServerWithImageDir).
func newTestServerWithActivityLog(t *testing.T) (*httptest.Server, *sqlite.Store) {
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/ActivityLog/Entries", requireAdmin(srv.handleActivityLogEntries))
	// Auth needs its own route too since loginToken/authenticate go
	// through srv, not this local mux — this also exercises
	// logAuthSuccess/logAuthFailure for real (auth_by_name.go), which is
	// exactly what this file's tests want to observe.
	mux.HandleFunc("POST /Users/AuthenticateByName", srv.handleAuthenticateByName)

	handler := withCORS(srv.withAuthContext(withCaseInsensitivePaths(mux)))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, st
}

// TestActivityLogEntriesRequiresAdmin regression-guards that GET
// /System/ActivityLog/Entries needs a valid token at all.
func TestActivityLogEntriesRequiresAdmin(t *testing.T) {
	ts, _ := newTestServerWithActivityLog(t)

	noAuthResp, err := http.Get(ts.URL + "/System/ActivityLog/Entries")
	if err != nil {
		t.Fatalf("GET /System/ActivityLog/Entries (no auth): %v", err)
	}
	defer noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", noAuthResp.StatusCode)
	}
}

// TestActivityLogEntriesRequiresAdminNotJustAuth regression-guards that a
// plain authenticated (non-admin) user is rejected with 403, not let
// through — same convention as every other admin-only route in this
// package (auth.go's requireAdmin doc comment).
func TestActivityLogEntriesRequiresAdminNotJustAuth(t *testing.T) {
	ts, st := newTestServerWithActivityLog(t)
	user := createTestUser(t, st, "regular", "regularpass")
	token := loginToken(t, ts.URL, user.Name, "regularpass")
	get := authedClient(t, ts.URL, token)

	resp := get(http.MethodGet, "/System/ActivityLog/Entries")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", resp.StatusCode)
	}
}

// TestActivityLogEntriesQueryParamsAndShape covers startIndex/limit paging,
// minDate, and hasUserId — the exact query params confirmed live against a
// real Jellyfin server (handleActivityLogEntries' doc comment) — plus the
// response envelope shape (Items/TotalRecordCount/StartIndex) and that a
// failed-login entry carries no UserId key at all on the wire.
func TestActivityLogEntriesQueryParamsAndShape(t *testing.T) {
	ts, st := newTestServerWithActivityLog(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass") // appends one SessionStarted entry, for real
	get := authedClient(t, ts.URL, token)

	// Seed an older failed-login entry directly through the store.
	ctx := context.Background()
	oldFailed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.AppendActivityEntry(ctx, storage.ActivityEntry{
		Name:     "Failed login attempt by ghost",
		Type:     storage.ActivityTypeAuthenticationFailed,
		Date:     oldFailed,
		Severity: storage.ActivitySeverityError,
	}); err != nil {
		t.Fatalf("AppendActivityEntry (failed): %v", err)
	}

	// hasUserId=true should surface the admin's own just-logged-in
	// SessionStarted entry, and only that one.
	resp := get(http.MethodGet, "/System/ActivityLog/Entries?startIndex=0&limit=10&hasUserId=true")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result activityLogEntriesResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if result.StartIndex != 0 {
		t.Errorf("StartIndex = %d, want 0", result.StartIndex)
	}
	if len(result.Items) != 1 || result.TotalRecordCount != 1 {
		t.Fatalf("hasUserId=true: Items=%+v TotalRecordCount=%d, want 1 item", result.Items, result.TotalRecordCount)
	}
	entry := result.Items[0]
	if entry.Type != string(storage.ActivityTypeSessionStarted) {
		t.Errorf("Type = %q, want %q", entry.Type, storage.ActivityTypeSessionStarted)
	}
	if entry.UserId == nil || *entry.UserId != string(admin.ID) {
		t.Errorf("UserId = %v, want %q", entry.UserId, admin.ID)
	}
	if entry.Severity != string(storage.ActivitySeverityInfo) {
		t.Errorf("Severity = %q, want %q", entry.Severity, storage.ActivitySeverityInfo)
	}
	if entry.Id == 0 {
		t.Error("Id = 0, want a real auto-increment id")
	}

	// hasUserId=false should return only the seeded failed login, with no
	// UserId key at all on the wire (json.RawMessage-free raw decode, so
	// "absent" and "present but null" are distinguishable).
	resp2 := get(http.MethodGet, "/System/ActivityLog/Entries?startIndex=0&limit=10&hasUserId=false")
	defer resp2.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&raw); err != nil {
		t.Fatalf("decoding raw response: %v", err)
	}
	items, _ := raw["Items"].([]any)
	if len(items) != 1 {
		t.Fatalf("hasUserId=false: len(Items) = %d, want 1", len(items))
	}
	first, _ := items[0].(map[string]any)
	if _, ok := first["UserId"]; ok {
		t.Errorf("hasUserId=false entry has a UserId key: %v", first["UserId"])
	}
	if first["Name"] != "Failed login attempt by ghost" {
		t.Errorf("Name = %v, want %q", first["Name"], "Failed login attempt by ghost")
	}

	// minDate excludes the old seeded failed entry.
	minDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	resp3 := get(http.MethodGet, "/System/ActivityLog/Entries?startIndex=0&limit=10&minDate="+minDate)
	defer resp3.Body.Close()
	var result3 activityLogEntriesResult
	if err := json.NewDecoder(resp3.Body).Decode(&result3); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	for _, e := range result3.Items {
		if e.Name == "Failed login attempt by ghost" {
			t.Errorf("minDate filter should have excluded the old seeded entry")
		}
	}

	// startIndex/limit paging: limit=1 with two total entries should
	// still report the true pre-pagination TotalRecordCount.
	resp4 := get(http.MethodGet, "/System/ActivityLog/Entries?startIndex=0&limit=1")
	defer resp4.Body.Close()
	var result4 activityLogEntriesResult
	if err := json.NewDecoder(resp4.Body).Decode(&result4); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result4.Items) != 1 {
		t.Fatalf("limit=1: len(Items) = %d, want 1", len(result4.Items))
	}
	if result4.TotalRecordCount != 2 {
		t.Errorf("TotalRecordCount = %d, want 2 (pre-pagination match count)", result4.TotalRecordCount)
	}
}
