package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// adminClient returns a helper that issues an authenticated admin request
// against ts, optionally with a JSON body (nil for none) — same shape as
// TestUsersListCreateDelete's authedReq in api_test.go, factored out here
// since every test in this file needs it (library management is
// admin-only, see auth.go's requireAdmin).
func adminClient(t *testing.T, ts string, token string) func(method, path string, body any) *http.Response {
	t.Helper()
	return func(method, path string, body any) *http.Response {
		t.Helper()
		var r *http.Request
		var err error
		if body != nil {
			b, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatalf("marshaling body: %v", marshalErr)
			}
			r, err = http.NewRequest(method, ts+path, bytes.NewReader(b))
			if err == nil {
				r.Header.Set("Content-Type", "application/json")
			}
		} else {
			r, err = http.NewRequest(method, ts+path, nil)
		}
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		r.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
}

// callHandlerDirect invokes handler with a request built from method+target
// (target may include a query string) and, if body is non-nil, a JSON body.
// POST /Library/VirtualFolders/LibraryOptions and POST
// /Library/VirtualFolders/Name aren't wired into server.go's route table
// yet (registering routes there is left to a separate change, per this
// task's scope — see the caller for the exact HandleFunc lines to add), so
// their handlers are exercised directly instead of through an
// httptest.Server round trip.
func callHandlerDirect(t *testing.T, handler http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling body: %v", err)
		}
		req = httptest.NewRequest(method, target, bytes.NewReader(b))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestAddVirtualFolder_BodyPathInfosWins(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := adminClient(t, ts.URL, token)

	body := map[string]any{
		"LibraryOptions": map[string]any{
			"PathInfos": []map[string]string{{"Path": "/media/movies"}},
		},
	}
	resp := post(http.MethodPost, "/Library/VirtualFolders?collectionType=movies&refreshLibrary=false&name=Movies", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	libs, err := st.QueryLibraries(t.Context())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("len(libs) = %d, want 1", len(libs))
	}
	if got := libs[0].Paths; len(got) != 1 || got[0] != "/media/movies" {
		t.Errorf("Paths = %v, want [/media/movies]", got)
	}
}

func TestAddVirtualFolder_BodyPathsOverrideQueryPaths(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := adminClient(t, ts.URL, token)

	body := map[string]any{
		"LibraryOptions": map[string]any{
			"PathInfos": []map[string]string{{"Path": "/media/from-body"}},
		},
	}
	resp := post(http.MethodPost, "/Library/VirtualFolders?collectionType=movies&name=Movies&paths=/media/from-query", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	libs, err := st.QueryLibraries(t.Context())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 || len(libs[0].Paths) != 1 || libs[0].Paths[0] != "/media/from-body" {
		t.Errorf("Paths = %v, want [/media/from-body] (body should win over query)", libs[0].Paths)
	}
}

func TestAddVirtualFolder_QueryPathsFallbackWhenNoBody(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := adminClient(t, ts.URL, token)

	resp := post(http.MethodPost, "/Library/VirtualFolders?collectionType=movies&name=Movies&paths=/media/a&paths=/media/b", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	libs, err := st.QueryLibraries(t.Context())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 || len(libs[0].Paths) != 2 {
		t.Fatalf("Paths = %v, want 2 entries from query fallback", libs[0].Paths)
	}
}

func TestAddVirtualFolder_NoPathsAtAll(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := adminClient(t, ts.URL, token)

	resp := post(http.MethodPost, "/Library/VirtualFolders?collectionType=movies&name=Empty", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (no paths anywhere shouldn't error)", resp.StatusCode)
	}

	libs, err := st.QueryLibraries(t.Context())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 || len(libs[0].Paths) != 0 {
		t.Fatalf("Paths = %v, want empty", libs[0].Paths)
	}
}

func TestAddVirtualFolder_MissingNameStillBadRequest(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	post := adminClient(t, ts.URL, token)

	resp := post(http.MethodPost, "/Library/VirtualFolders?collectionType=movies", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRemoveVirtualFolder_StillWorksByName(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	client := adminClient(t, ts.URL, token)

	createTestLibrary(t, st, "ToDelete", storage.CollectionTypeMovies)

	resp := client(http.MethodDelete, "/Library/VirtualFolders?name=ToDelete", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	libs, err := st.QueryLibraries(t.Context())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 0 {
		t.Fatalf("len(libs) = %d, want 0", len(libs))
	}

	resp2 := client(http.MethodDelete, "/Library/VirtualFolders?name=DoesNotExist", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp2.StatusCode)
	}
}

func TestSaveLibraryOptions_RoundTripsThroughGet(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	client := adminClient(t, ts.URL, token)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	body := map[string]any{
		"Id": string(libID),
		"LibraryOptions": map[string]any{
			"EnablePhotos":          true,
			"EnableRealtimeMonitor": false,
			"PathInfos":             []map[string]string{{"Path": "/media/Movies"}},
		},
	}
	w := callHandlerDirect(t, srv.handleSaveLibraryOptions, http.MethodPost, "/Library/VirtualFolders/LibraryOptions", body)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	getResp := client(http.MethodGet, "/Library/VirtualFolders", nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}
	var folders []virtualFolderInfo
	if err := json.NewDecoder(getResp.Body).Decode(&folders); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
	if folders[0].LibraryOptions == nil {
		t.Fatalf("LibraryOptions = nil, want the saved blob echoed back")
	}
	var echoed struct {
		EnablePhotos bool `json:"EnablePhotos"`
	}
	if err := json.Unmarshal(folders[0].LibraryOptions, &echoed); err != nil {
		t.Fatalf("unmarshaling echoed LibraryOptions: %v", err)
	}
	if !echoed.EnablePhotos {
		t.Errorf("echoed EnablePhotos = false, want true (round-trip lost data)")
	}
}

func TestSaveLibraryOptions_UnknownIdIs404(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	body := map[string]any{"Id": "does-not-exist", "LibraryOptions": map[string]any{}}
	w := callHandlerDirect(t, srv.handleSaveLibraryOptions, http.MethodPost, "/Library/VirtualFolders/LibraryOptions", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestRenameVirtualFolder_Success(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	libID := createTestLibrary(t, st, "OldName", storage.CollectionTypeMovies)

	w := callHandlerDirect(t, srv.handleRenameVirtualFolder, http.MethodPost, "/Library/VirtualFolders/Name?name=OldName&newName=NewName&refreshLibrary=false", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	lib, err := st.GetLibrary(t.Context(), libID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if lib.Name != "NewName" {
		t.Errorf("Name = %q, want %q", lib.Name, "NewName")
	}
	if len(lib.Paths) != 1 || lib.Paths[0] != "/media/OldName" {
		t.Errorf("Paths = %v, want unchanged from creation", lib.Paths)
	}
}

func TestRenameVirtualFolder_UnknownNameIs404(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	w := callHandlerDirect(t, srv.handleRenameVirtualFolder, http.MethodPost, "/Library/VirtualFolders/Name?name=DoesNotExist&newName=Whatever", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestRenameVirtualFolder_EmptyNewNameIs400(t *testing.T) {
	_, st := newTestServer(t)
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})

	createTestLibrary(t, st, "SomeLib", storage.CollectionTypeMovies)

	w := callHandlerDirect(t, srv.handleRenameVirtualFolder, http.MethodPost, "/Library/VirtualFolders/Name?name=SomeLib&newName=", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
