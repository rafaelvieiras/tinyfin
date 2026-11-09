package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newTestServerWithImageDir is newTestServer (api_test.go) plus a
// ScanImageDir — handleUploadItemImage needs somewhere to write files,
// which newTestServer's zero-value Config leaves unset.
//
// It serves through a locally built mux rather than srv.Handler(): the
// three routes these handlers answer to
// (GET/POST/DELETE .../Items/{itemId}/Images...) are deliberately not
// registered in server.go yet (see this file's package doc comment for
// why — route wiring is a separate, coordinated change touching a file
// this task must not edit). The middleware stack (withAuthContext/
// withCORS/withCaseInsensitivePaths) is the same s.Handler() applies in
// production, just wrapped around this test-only mux instead, so
// requireAdmin and auth context resolution are exercised for real.
func newTestServerWithImageDir(t *testing.T) (*httptest.Server, *sqlite.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	imgDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	srv := NewServer(st, Config{ServerName: "TestServer", ScanImageDir: imgDir})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /Items/{itemId}/Images", requireAdmin(srv.handleListItemImages))
	// The byte-serving route an image URL actually hits (unlike the admin
	// list/upload/delete above, it takes no token: it backs a bare <img src>).
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", srv.handleItemImage)
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", srv.handleItemImage)
	mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}", requireAdmin(srv.handleUploadItemImage))
	mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}/{imageIndex}", requireAdmin(srv.handleUploadItemImage))
	mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}", requireAdmin(srv.handleDeleteItemImage))
	mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}/{imageIndex}", requireAdmin(srv.handleDeleteItemImage))
	// Auth needs its own routes too since the reference AuthenticateByName
	// flow (loginToken) goes through srv, not this local mux.
	mux.HandleFunc("POST /Users/AuthenticateByName", srv.handleAuthenticateByName)

	handler := withCORS(srv.withAuthContext(withCaseInsensitivePaths(mux)))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, st, imgDir
}

// authedRequest issues an authenticated request with an optional body and
// Content-Type, returning the raw *http.Response — authedClient
// (library_test.go) doesn't support a request body, which the upload
// handler needs.
func authedRequest(t *testing.T, ts, token, method, path, contentType string, body []byte) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// testPNGBase64 is a minimal valid 1x1 PNG, base64-encoded — the shape
// confirmed live against the reference server for a POST .../Images/...
// body (raw base64, not multipart).
func testPNGBase64(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return []byte(encoded)
}

func TestListItemImagesEmpty(t *testing.T) {
	ts, st, _ := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "img-admin-empty", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "NoImagesLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Imageless Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Imageless Movie")

	resp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(itemID)+"/Images", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../Images status = %d, want 200", resp.StatusCode)
	}
	var got []imageInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 (honest empty, not 404)", len(got))
	}
}

func TestListItemImagesWithExisting(t *testing.T) {
	ts, st, _ := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "img-admin-list", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "ImagesLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Imaged Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Imaged Movie")

	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID, Kind: "Primary", Path: "poster.jpg", Tag: "tag-primary", Width: 300, Height: 450, Size: 111,
	}); err != nil {
		t.Fatalf("UpsertImageRef(Primary): %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID, Kind: "Backdrop", Index: 0, Path: "fanart0.jpg", Tag: "tag-bd0", Size: 222,
	}); err != nil {
		t.Fatalf("UpsertImageRef(Backdrop 0): %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID, Kind: "Backdrop", Index: 1, Path: "fanart1.jpg", Tag: "tag-bd1", Size: 333,
	}); err != nil {
		t.Fatalf("UpsertImageRef(Backdrop 1): %v", err)
	}

	resp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(itemID)+"/Images", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../Images status = %d, want 200", resp.StatusCode)
	}
	var got []imageInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	byTag := make(map[string]imageInfo, len(got))
	for _, info := range got {
		byTag[info.ImageTag] = info
	}

	primary, ok := byTag["tag-primary"]
	if !ok {
		t.Fatal("missing Primary entry")
	}
	if primary.ImageType != "Primary" || primary.Width != 300 || primary.Height != 450 || primary.Size != 111 {
		t.Errorf("primary = %+v, unexpected fields", primary)
	}
	if primary.ImageIndex != nil {
		t.Errorf("primary.ImageIndex = %v, want nil (single-instance kind)", *primary.ImageIndex)
	}

	bd0, ok := byTag["tag-bd0"]
	if !ok {
		t.Fatal("missing Backdrop index 0 entry")
	}
	if bd0.ImageIndex == nil || *bd0.ImageIndex != 0 {
		t.Errorf("bd0.ImageIndex = %v, want 0", bd0.ImageIndex)
	}

	bd1, ok := byTag["tag-bd1"]
	if !ok {
		t.Fatal("missing Backdrop index 1 entry")
	}
	if bd1.ImageIndex == nil || *bd1.ImageIndex != 1 {
		t.Errorf("bd1.ImageIndex = %v, want 1", bd1.ImageIndex)
	}
}

func TestUploadItemImageThenAppearsInList(t *testing.T) {
	ts, st, imgDir := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "img-admin-upload", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "UploadLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Upload Target Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Upload Target Movie")

	body := testPNGBase64(t)
	resp := authedRequest(t, ts.URL, token, http.MethodPost, "/Items/"+string(itemID)+"/Images/Primary", "image/png", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST .../Images/Primary status = %d, want 204", resp.StatusCode)
	}

	refs, err := st.GetImageRefs(context.Background(), itemID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	if refs[0].Width != 1 || refs[0].Height != 1 {
		t.Errorf("refs[0] dimensions = %dx%d, want 1x1", refs[0].Width, refs[0].Height)
	}
	if _, err := os.Stat(refs[0].Path); err != nil {
		t.Errorf("uploaded file not on disk at %q: %v", refs[0].Path, err)
	}
	if filepath.Dir(refs[0].Path) != imgDir {
		t.Errorf("uploaded file dir = %q, want %q", filepath.Dir(refs[0].Path), imgDir)
	}

	// And it shows up through the list endpoint too.
	listResp := authedRequest(t, ts.URL, token, http.MethodGet, "/Items/"+string(itemID)+"/Images", "", nil)
	defer listResp.Body.Close()
	var list []imageInfo
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(list) != 1 || list[0].ImageType != "Primary" {
		t.Fatalf("list = %+v, want one Primary entry", list)
	}
}

func TestDeleteItemImage(t *testing.T) {
	ts, st, _ := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "img-admin-delete", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	libID := createTestLibrary(t, st, "DeleteLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Delete Target Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Delete Target Movie")

	imgPath := filepath.Join(t.TempDir(), "poster.jpg")
	if err := os.WriteFile(imgPath, []byte("fake-jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID, Kind: "Primary", Path: imgPath, Tag: "tag-1",
	}); err != nil {
		t.Fatalf("UpsertImageRef: %v", err)
	}

	// Deleting a nonexistent image (wrong kind) is a 204 no-op, matching the
	// reference server's idempotent behavior.
	missResp := authedRequest(t, ts.URL, token, http.MethodDelete, "/Items/"+string(itemID)+"/Images/Logo", "", nil)
	defer missResp.Body.Close()
	if missResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE .../Images/Logo (nonexistent) status = %d, want 204", missResp.StatusCode)
	}

	// Deleting the real one succeeds and removes both the ref and the file.
	delResp := authedRequest(t, ts.URL, token, http.MethodDelete, "/Items/"+string(itemID)+"/Images/Primary", "", nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE .../Images/Primary status = %d, want 204", delResp.StatusCode)
	}
	refs, err := st.GetImageRefs(context.Background(), itemID, "Primary")
	if err != nil {
		t.Fatalf("GetImageRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0 after delete", len(refs))
	}
	if _, err := os.Stat(imgPath); !os.IsNotExist(err) {
		t.Errorf("file at %q still exists after delete", imgPath)
	}

	// Deleting again (now truly gone) is still 204, idempotently.
	againResp := authedRequest(t, ts.URL, token, http.MethodDelete, "/Items/"+string(itemID)+"/Images/Primary", "", nil)
	defer againResp.Body.Close()
	if againResp.StatusCode != http.StatusNoContent {
		t.Fatalf("second DELETE .../Images/Primary status = %d, want 204", againResp.StatusCode)
	}
}

func TestItemImagesRequireAdmin(t *testing.T) {
	ts, st, _ := newTestServerWithImageDir(t)
	admin := createTestAdminUser(t, st, "img-admin-perm", "adminpass")
	user := createTestUser(t, st, "img-user-perm", "userpass")
	adminToken := loginToken(t, ts.URL, admin.Name, "adminpass")
	userToken := loginToken(t, ts.URL, user.Name, "userpass")

	libID := createTestLibrary(t, st, "PermLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Perm Movie",
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Perm Movie")
	imagesPath := "/Items/" + string(itemID) + "/Images"

	cases := []struct {
		method, path, contentType string
		body                      []byte
	}{
		{http.MethodGet, imagesPath, "", nil},
		{http.MethodPost, imagesPath + "/Primary", "image/png", testPNGBase64(t)},
		{http.MethodDelete, imagesPath + "/Primary", "", nil},
	}

	for _, tc := range cases {
		// No token at all: 401.
		noAuthReq, err := http.NewRequest(tc.method, ts.URL+tc.path, bytes.NewReader(tc.body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		if tc.contentType != "" {
			noAuthReq.Header.Set("Content-Type", tc.contentType)
		}
		noAuthResp, err := http.DefaultClient.Do(noAuthReq)
		if err != nil {
			t.Fatalf("%s %s (no auth): %v", tc.method, tc.path, err)
		}
		noAuthResp.Body.Close()
		if noAuthResp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s (no auth) status = %d, want 401", tc.method, tc.path, noAuthResp.StatusCode)
		}

		// Authenticated but non-admin: 403.
		userResp := authedRequest(t, ts.URL, userToken, tc.method, tc.path, tc.contentType, tc.body)
		userResp.Body.Close()
		if userResp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (non-admin) status = %d, want 403", tc.method, tc.path, userResp.StatusCode)
		}

		// Admin: not 401/403 (may legitimately 404/204/200 depending on state).
		adminResp := authedRequest(t, ts.URL, adminToken, tc.method, tc.path, tc.contentType, tc.body)
		adminResp.Body.Close()
		if adminResp.StatusCode == http.StatusUnauthorized || adminResp.StatusCode == http.StatusForbidden {
			t.Errorf("%s %s (admin) status = %d, want neither 401 nor 403", tc.method, tc.path, adminResp.StatusCode)
		}
	}
}
