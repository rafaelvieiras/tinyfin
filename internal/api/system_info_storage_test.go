package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newStorageTestServer is newTestServer (api_test.go) plus a caller-supplied
// DataDir/WebDir — GET /System/Info/Storage isn't wired into server.go's
// route table yet (that wiring is a separate, deliberately out-of-scope
// change here, same as library_admin_test.go's callHandlerDirect note), so
// these tests call handleSystemInfoStorage directly rather than through an
// httptest.Server round trip; a real *sqlite.Store is still used (not a
// fake) since the handler's Libraries section depends on real
// QueryLibraries behavior.
func newStorageTestServer(t *testing.T, dataDir, webDir string) (*Server, *sqlite.Store) {
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
	srv := startTestServer(t, st, Config{ServerName: "TestServer", DataDir: dataDir, WebDir: webDir})
	return srv, st
}

func TestSystemInfoStorage_DataDirFolders(t *testing.T) {
	dataDir := t.TempDir()
	srv, _ := newStorageTestServer(t, dataDir, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/System/Info/Storage", nil)
	srv.handleSystemInfoStorage(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got systemStorageInfo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// ProgramDataFolder/CacheFolder/LogFolder/InternalMetadataFolder/
	// ImageCacheFolder all genuinely point at the same DataDir in this
	// project (system_info_storage.go's package doc comment) — assert all
	// five report the identical, non-zero-Free/UsedSpace stat.
	folders := map[string]folderStorageInfo{
		"ProgramDataFolder":      got.ProgramDataFolder,
		"CacheFolder":            got.CacheFolder,
		"LogFolder":              got.LogFolder,
		"InternalMetadataFolder": got.InternalMetadataFolder,
		"ImageCacheFolder":       got.ImageCacheFolder,
	}
	for name, f := range folders {
		if f.Path != dataDir {
			t.Errorf("%s.Path = %q, want %q", name, f.Path, dataDir)
		}
		if f.DeviceId != dataDir {
			t.Errorf("%s.DeviceId = %q, want %q", name, f.DeviceId, dataDir)
		}
		if f.StorageType != "Fixed" {
			t.Errorf("%s.StorageType = %q, want %q", name, f.StorageType, "Fixed")
		}
		if f.FreeSpace <= 0 {
			t.Errorf("%s.FreeSpace = %d, want > 0", name, f.FreeSpace)
		}
		if f.UsedSpace <= 0 {
			t.Errorf("%s.UsedSpace = %d, want > 0", name, f.UsedSpace)
		}
	}

	// TranscodingTempFolder is os.TempDir(), always present regardless of
	// WebDir.
	if got.TranscodingTempFolder.FreeSpace <= 0 || got.TranscodingTempFolder.UsedSpace <= 0 {
		t.Errorf("TranscodingTempFolder = %+v, want non-zero Free/UsedSpace", got.TranscodingTempFolder)
	}
}

func TestSystemInfoStorage_WebFolderAbsentWhenUnset(t *testing.T) {
	srv, _ := newStorageTestServer(t, t.TempDir(), "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/System/Info/Storage", nil)
	srv.handleSystemInfoStorage(w, r)

	// Assert the raw JSON has no "WebFolder" key at all (not present-but-null)
	// — the *folderStorageInfo,omitempty field should elide entirely, per
	// this task's requirement that a no-bundled-web-UI deployment not
	// report a stat for a directory that doesn't exist.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshaling raw response: %v", err)
	}
	if _, ok := raw["WebFolder"]; ok {
		t.Errorf("WebFolder key present in JSON, want absent entirely: %s", w.Body.String())
	}
}

func TestSystemInfoStorage_WebFolderPresentWhenSet(t *testing.T) {
	webDir := t.TempDir()
	srv, _ := newStorageTestServer(t, t.TempDir(), webDir)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/System/Info/Storage", nil)
	srv.handleSystemInfoStorage(w, r)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshaling raw response: %v", err)
	}
	if _, ok := raw["WebFolder"]; !ok {
		t.Fatalf("WebFolder key absent from JSON, want present: %s", w.Body.String())
	}

	var got systemStorageInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.WebFolder == nil {
		t.Fatal("WebFolder = nil, want non-nil")
	}
	if got.WebFolder.Path != webDir || got.WebFolder.DeviceId != webDir {
		t.Errorf("WebFolder = %+v, want Path/DeviceId = %q", got.WebFolder, webDir)
	}
	if got.WebFolder.FreeSpace <= 0 || got.WebFolder.UsedSpace <= 0 {
		t.Errorf("WebFolder = %+v, want non-zero Free/UsedSpace", got.WebFolder)
	}
}

func TestSystemInfoStorage_Libraries(t *testing.T) {
	srv, st := newStorageTestServer(t, t.TempDir(), "")

	moviesDir := t.TempDir()
	if err := st.UpsertLibrary(context.Background(), storage.Library{
		Name:           "Movies",
		CollectionType: storage.CollectionTypeMovies,
		Paths:          []string{moviesDir},
	}); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	libs, err := st.QueryLibraries(context.Background())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("len(libs) = %d, want 1", len(libs))
	}
	wantID := string(libs[0].ID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/System/Info/Storage", nil)
	srv.handleSystemInfoStorage(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got systemStorageInfo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Libraries) != 1 {
		t.Fatalf("len(Libraries) = %d, want 1", len(got.Libraries))
	}
	lib := got.Libraries[0]
	if lib.Id != wantID {
		t.Errorf("Libraries[0].Id = %q, want %q", lib.Id, wantID)
	}
	if lib.Name != "Movies" {
		t.Errorf("Libraries[0].Name = %q, want %q", lib.Name, "Movies")
	}
	if len(lib.Folders) != 1 {
		t.Fatalf("len(Libraries[0].Folders) = %d, want 1", len(lib.Folders))
	}
	folder := lib.Folders[0]
	if folder.Path != moviesDir || folder.DeviceId != moviesDir {
		t.Errorf("folder = %+v, want Path/DeviceId = %q", folder, moviesDir)
	}
	if folder.StorageType != "Fixed" {
		t.Errorf("folder.StorageType = %q, want %q", folder.StorageType, "Fixed")
	}
	if folder.FreeSpace <= 0 {
		t.Errorf("folder.FreeSpace = %d, want > 0", folder.FreeSpace)
	}
	if folder.UsedSpace <= 0 {
		t.Errorf("folder.UsedSpace = %d, want > 0", folder.UsedSpace)
	}
}
