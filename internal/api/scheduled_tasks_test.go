package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newTestServerWithScheduledTasks is newTestServer (api_test.go) plus a
// locally built mux exposing GET /ScheduledTasks — deliberately not
// registered in server.go yet (route wiring is a separate, coordinated
// change touching a file this task must not edit, same reasoning as
// item_images_admin_test.go's newTestServerWithImageDir). Returns the
// *sqlite.Store and *Server too: the store to seed users, srv to drive
// srv.scanState directly (begin/end are unexported but reachable from a
// test in the same package) so a scan in flight/finished can be simulated
// without a real filesystem scan.
func newTestServerWithScheduledTasks(t *testing.T) (*httptest.Server, *sqlite.Store, *Server) {
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
	mux.HandleFunc("GET /ScheduledTasks", requireAdmin(srv.handleScheduledTasks))
	mux.HandleFunc("POST /ScheduledTasks/{first}/{second}", requireAdmin(srv.handleScheduledTaskPost))
	// Auth needs its own route too since the reference AuthenticateByName
	// flow (loginToken) goes through srv, not this local mux.
	mux.HandleFunc("GET /ScheduledTasks/{taskId}", requireAdmin(srv.handleScheduledTaskGet))
	mux.HandleFunc("DELETE /ScheduledTasks/Running/{taskId}", requireAdmin(srv.handleScheduledTaskStop))
	mux.HandleFunc("POST /Users/AuthenticateByName", srv.handleAuthenticateByName)

	handler := withCORS(srv.withAuthContext(withCaseInsensitivePaths(mux)))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, st, srv
}

// decodeScheduledTasks decodes resp's body into the single-entry task list
// this handler always returns, failing the test on any decode/shape error.
func decodeScheduledTasks(t *testing.T, resp *http.Response) []scheduledTaskInfo {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ScheduledTasks status = %d, want 200", resp.StatusCode)
	}
	var tasks []scheduledTaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// The list carries every real task; these tests are about the library scan.
	for _, task := range tasks {
		if task.Key == "RefreshLibrary" {
			return []scheduledTaskInfo{task}
		}
	}
	t.Fatalf("no RefreshLibrary task in %+v", tasks)
	return nil
}

// TestScheduledTasksNoScanEverRun covers a fresh process where no library
// scan has run yet: a single Idle entry, with LastExecutionResult entirely
// absent (not a fabricated one) since no scan has finished this process's
// lifetime.
func TestScheduledTasksNoScanEverRun(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	tasks := decodeScheduledTasks(t, get(http.MethodGet, "/ScheduledTasks"))
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.State != "Idle" {
		t.Errorf("State = %q, want %q", task.State, "Idle")
	}
	if task.LastExecutionResult != nil {
		t.Errorf("LastExecutionResult = %+v, want nil (no scan has run yet)", task.LastExecutionResult)
	}
	if task.Id != scheduledTaskLibraryScanID || task.Key != "RefreshLibrary" {
		t.Errorf("Id/Key = %q/%q, want %q/RefreshLibrary", task.Id, task.Key, scheduledTaskLibraryScanID)
	}
	if task.Category != "Library" {
		t.Errorf("Category = %q, want %q", task.Category, "Library")
	}
	if task.IsHidden {
		t.Error("IsHidden = true, want false")
	}
	if len(task.Triggers) != 1 || task.Triggers[0].Type != "IntervalTrigger" {
		t.Errorf("Triggers = %+v, want the default 12h IntervalTrigger", task.Triggers)
	}
	if task.Name == "" || task.Description == "" {
		t.Error("Name/Description should not be empty")
	}
}

// TestScheduledTasksScanRunning covers a scan currently in flight: State
// must report "Running" regardless of whether any scan has ever finished
// before.
func TestScheduledTasksScanRunning(t *testing.T) {
	ts, st, srv := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	srv.scanState.begin()
	defer srv.scanState.end(nil)

	tasks := decodeScheduledTasks(t, get(http.MethodGet, "/ScheduledTasks"))
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].State != "Running" {
		t.Errorf("State = %q, want %q", tasks[0].State, "Running")
	}
}

// TestScheduledTasksScanFinishedSuccess covers a scan that has already
// finished successfully: State goes back to Idle, and LastExecutionResult
// is now populated with Status "Completed".
func TestScheduledTasksScanFinishedSuccess(t *testing.T) {
	ts, st, srv := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	srv.scanState.begin()
	srv.scanState.end(nil)

	tasks := decodeScheduledTasks(t, get(http.MethodGet, "/ScheduledTasks"))
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.State != "Idle" {
		t.Errorf("State = %q, want %q", task.State, "Idle")
	}
	if task.LastExecutionResult == nil {
		t.Fatal("LastExecutionResult = nil, want populated (a scan has finished)")
	}
	if task.LastExecutionResult.Status != "Completed" {
		t.Errorf("LastExecutionResult.Status = %q, want %q", task.LastExecutionResult.Status, "Completed")
	}
	if task.LastExecutionResult.StartTimeUtc == "" || task.LastExecutionResult.EndTimeUtc == "" {
		t.Error("LastExecutionResult.StartTimeUtc/EndTimeUtc should not be empty")
	}
	if task.LastExecutionResult.Id != scheduledTaskLibraryScanID || task.LastExecutionResult.Key != "RefreshLibrary" {
		t.Errorf("LastExecutionResult.Id/Key = %q/%q, want %q/RefreshLibrary",
			task.LastExecutionResult.Id, task.LastExecutionResult.Key, scheduledTaskLibraryScanID)
	}
}

// TestScheduledTasksScanFinishedFailure covers a scan that finished with an
// error: LastExecutionResult.Status must report "Failed", not "Completed".
func TestScheduledTasksScanFinishedFailure(t *testing.T) {
	ts, st, srv := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	srv.scanState.begin()
	srv.scanState.end(errors.New("boom"))

	tasks := decodeScheduledTasks(t, get(http.MethodGet, "/ScheduledTasks"))
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.State != "Idle" {
		t.Errorf("State = %q, want %q (the scan finished, even though it failed)", task.State, "Idle")
	}
	if task.LastExecutionResult == nil {
		t.Fatal("LastExecutionResult = nil, want populated (a scan has finished)")
	}
	if task.LastExecutionResult.Status != "Failed" {
		t.Errorf("LastExecutionResult.Status = %q, want %q", task.LastExecutionResult.Status, "Failed")
	}
}

// TestScheduledTasksRequiresAdmin covers the auth gate: no session at all
// gets 401, and an authenticated but non-admin user gets 403 — same pattern
// requireAdmin's own doc comment (auth.go) describes for library_admin.go's
// sibling routes.
func TestScheduledTasksRequiresAdmin(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)

	noAuthResp, err := http.Get(ts.URL + "/ScheduledTasks")
	if err != nil {
		t.Fatalf("GET /ScheduledTasks (no auth): %v", err)
	}
	defer noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", noAuthResp.StatusCode)
	}

	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	nonAdminResp := get(http.MethodGet, "/ScheduledTasks")
	defer nonAdminResp.Body.Close()
	if nonAdminResp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", nonAdminResp.StatusCode)
	}
}

// TestScheduledTaskStartWrongIdNotFound covers the reason the Dashboard's
// "Scan All Libraries" button used to spin forever: jellyfin-web locates the
// task by filtering GET /ScheduledTasks on Key === "RefreshLibrary", then
// starts it by Id at this route. Any other taskId addresses a task this
// project doesn't have.
func TestScheduledTaskStartWrongIdNotFound(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := authedClient(t, ts.URL, token)

	resp := post(http.MethodPost, "/ScheduledTasks/Running/some-other-task")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestScheduledTaskStartRefreshLibrary covers the fixed, working path: the
// exact Id/Key value jellyfin-web hardcodes ("RefreshLibrary") queues a scan
// and returns 204, same as POST /Library/Refresh.
func TestScheduledTaskStartRefreshLibrary(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := authedClient(t, ts.URL, token)

	resp := post(http.MethodPost, "/ScheduledTasks/Running/"+scheduledTaskLibraryScanID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

// TestScheduledTaskStartRequiresAdmin mirrors
// TestScheduledTasksRequiresAdmin for this route: no session gets 401, an
// authenticated non-admin gets 403.
func TestScheduledTaskStartRequiresAdmin(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)

	noAuthResp, err := http.Post(ts.URL+"/ScheduledTasks/Running/"+scheduledTaskLibraryScanID, "", nil)
	if err != nil {
		t.Fatalf("POST /ScheduledTasks/Running/%s (no auth): %v", scheduledTaskLibraryScanID, err)
	}
	defer noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", noAuthResp.StatusCode)
	}

	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	post := authedClient(t, ts.URL, token)

	nonAdminResp := post(http.MethodPost, "/ScheduledTasks/Running/"+scheduledTaskLibraryScanID)
	defer nonAdminResp.Body.Close()
	if nonAdminResp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", nonAdminResp.StatusCode)
	}
}

// TestItemRefreshDoesNotAffectScheduledTasks is a regression guard for a
// real bug: an earlier version of Server.runItemRefresh (item_refresh.go)
// shared scanState with runScan, so refreshing a single ordinary catalog
// item's metadata made this endpoint report the "Scan Media Libraries" task
// as Running — which is exactly what makes jellyfin-web's Dashboard "Scan
// All Libraries" button spin/disable itself (scheduledTaskLibraryScanID's
// doc comment), with no library scan running at all. a reporter caught this by
// asking whether a per-item job would show up as "scanning library" on the
// dashboard; checking jellyfin-web's own source confirmed the reference
// server never conflates the two (an item refresh is tracked entirely
// separately, never touching GET /ScheduledTasks or its ScheduledTasksInfo
// WebSocket push). runItemRefresh no longer touches scanState at all — this
// proves it stays that way.
func TestItemRefreshDoesNotAffectScheduledTasks(t *testing.T) {
	ts, st, srv := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Refresh Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Refresh Movie")

	// runItemRefresh is unexported but reachable directly (same package,
	// same as srv.scanState.begin/end elsewhere in this file) — called
	// synchronously here, unlike the real (goroutine-backed) HTTP route, so
	// the assertions below observe its aftermath deterministically.
	srv.runItemRefresh(itemID, false)

	running, lastStart, lastEnd, _ := srv.scanState.snapshot()
	if running {
		t.Error("scanState reports running after an item refresh, want untouched")
	}
	if !lastStart.IsZero() || !lastEnd.IsZero() {
		t.Errorf("scanState.lastStart/lastEnd = %v/%v, want zero (item refresh must never touch scanState)", lastStart, lastEnd)
	}

	tasks := decodeScheduledTasks(t, get(http.MethodGet, "/ScheduledTasks"))
	if len(tasks) != 1 || tasks[0].State != "Idle" || tasks[0].LastExecutionResult != nil {
		t.Errorf("ScheduledTasks after item refresh = %+v, want the one task still Idle with no LastExecutionResult", tasks)
	}
}

// TestScheduledTaskGetTriggersAndStop covers the per-task routes: GET by Id
// (404 on the Key), replacing triggers, rejecting an unknown trigger type and
// DELETE on a task that is not running.
func TestScheduledTaskGetTriggersAndStop(t *testing.T) {
	ts, st, _ := newTestServerWithScheduledTasks(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	req := func(method, path, body string) *http.Response {
		r, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		r.Header.Set("Authorization", `MediaBrowser Client="t", Device="t", DeviceId="t", Version="1", Token="`+token+`"`)
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	id := scheduledTaskLibraryScanID
	if got := req(http.MethodGet, "/ScheduledTasks/"+id, "").StatusCode; got != 200 {
		t.Fatalf("GET by Id = %d, want 200", got)
	}
	if got := req(http.MethodGet, "/ScheduledTasks/RefreshLibrary", "").StatusCode; got != 404 {
		t.Fatalf("GET by Key = %d, want 404", got)
	}
	if got := req(http.MethodPost, "/ScheduledTasks/"+id+"/Triggers", `[{"Type":"Bogus"}]`).StatusCode; got != 400 {
		t.Fatalf("bad trigger = %d, want 400", got)
	}
	if got := req(http.MethodPost, "/ScheduledTasks/"+id+"/Triggers", `[{"Type":"StartupTrigger"}]`).StatusCode; got != 204 {
		t.Fatalf("set triggers = %d, want 204", got)
	}
	resp := req(http.MethodGet, "/ScheduledTasks/"+id, "")
	var info scheduledTaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if len(info.Triggers) != 1 || info.Triggers[0].Type != "StartupTrigger" {
		t.Fatalf("Triggers = %+v, want one StartupTrigger", info.Triggers)
	}
	if got := req(http.MethodPost, "/ScheduledTasks/"+id+"/Triggers", `[]`).StatusCode; got != 204 {
		t.Fatalf("clear triggers = %d, want 204", got)
	}
	if got := req(http.MethodDelete, "/ScheduledTasks/Running/"+id, "").StatusCode; got != 204 {
		t.Fatalf("DELETE idle = %d, want 204", got)
	}
	if got := req(http.MethodDelete, "/ScheduledTasks/Running/00000000000000000000000000000000", "").StatusCode; got != 404 {
		t.Fatalf("DELETE unknown = %d, want 404", got)
	}
}
