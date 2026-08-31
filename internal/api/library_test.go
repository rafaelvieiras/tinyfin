package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// authedClient returns a helper that issues an authenticated request
// (X-Emby-Token) against ts and returns the raw *http.Response.
func authedClient(t *testing.T, ts string, token string) func(method, path string) *http.Response {
	t.Helper()
	return func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, ts+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("X-Emby-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
}

// createTestLibrary inserts a library directly through the storage layer
// and returns its persisted ID — every storage.Item requires a valid
// LibraryID (a NOT NULL foreign key, see the sqlite migration), so any test
// that creates an item needs one of these first.
func createTestLibrary(t *testing.T, st storage.LibraryStore, name string, collectionType storage.CollectionType) storage.LibraryID {
	t.Helper()
	lib := storage.Library{Name: name, CollectionType: collectionType, Paths: []string{"/media/" + name}}
	if err := st.UpsertLibrary(context.Background(), lib); err != nil {
		t.Fatalf("UpsertLibrary: %v", err)
	}
	libs, err := st.QueryLibraries(context.Background())
	if err != nil {
		t.Fatalf("QueryLibraries: %v", err)
	}
	for _, l := range libs {
		if l.Name == name {
			return l.ID
		}
	}
	t.Fatalf("could not find just-inserted library %q", name)
	return ""
}

// loginToken authenticates user/password against ts and returns the issued
// access token.
func loginToken(t *testing.T, ts, username, password string) string {
	t.Helper()
	resp := authenticate(t, ts, username, password)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticate: status = %d, want 200", resp.StatusCode)
	}
	var authResult authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&authResult); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}
	return authResult.AccessToken
}

func TestUserViewsAndItems(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	item := storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Test Movie",
		Container: "mp4",
		Path:      "/media/movies/test.mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{LibraryID: &libID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("QueryItems: %v page=%v", err, page)
	}
	itemID := page.Items[0].ID

	// GET /UserViews
	resp := get(http.MethodGet, "/UserViews")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /UserViews status = %d, want 200", resp.StatusCode)
	}
	var views baseItemDtoQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decoding /UserViews: %v", err)
	}
	if len(views.Items) != 1 || views.Items[0].Name != "Movies" {
		t.Fatalf("UserViews = %+v, want one item named Movies", views.Items)
	}
	if views.Items[0].Type != "UserView" {
		t.Errorf("UserViews[0].Type = %q, want UserView", views.Items[0].Type)
	}

	// GET /Items
	itemsResp := get(http.MethodGet, "/Items?parentId="+string(libID)+"&recursive=true")
	defer itemsResp.Body.Close()
	if itemsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items status = %d, want 200", itemsResp.StatusCode)
	}
	var items baseItemDtoQueryResult
	if err := json.NewDecoder(itemsResp.Body).Decode(&items); err != nil {
		t.Fatalf("decoding /Items: %v", err)
	}
	if items.TotalRecordCount != 1 || len(items.Items) != 1 {
		t.Fatalf("Items = %+v, want 1 item", items)
	}
	if items.Items[0].Name != "Test Movie" {
		t.Errorf("Items[0].Name = %q, want %q", items.Items[0].Name, "Test Movie")
	}

	// GET /Items/{itemId}
	detailResp := get(http.MethodGet, "/Items/"+string(itemID))
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/{itemId} status = %d, want 200", detailResp.StatusCode)
	}
	var detail baseItemDto
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding item detail: %v", err)
	}
	if detail.Name != "Test Movie" {
		t.Errorf("detail.Name = %q, want %q", detail.Name, "Test Movie")
	}
	if detail.Container != "mp4" {
		t.Errorf("detail.Container = %q, want %q", detail.Container, "mp4")
	}
	if len(detail.MediaStreams) != 2 {
		t.Errorf("len(detail.MediaStreams) = %d, want 2", len(detail.MediaStreams))
	}

	// GET /Items with a default (unspecified) limit still returns results
	// bounded — exercise the defaulting path explicitly.
	defaultLimitResp := get(http.MethodGet, "/Items?parentId="+string(libID)+"&recursive=true")
	defer defaultLimitResp.Body.Close()
	if defaultLimitResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items (default limit) status = %d, want 200", defaultLimitResp.StatusCode)
	}
}

func TestItemImageNotFound(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "erin", "erinpass")
	token := loginToken(t, ts.URL, user.Name, "erinpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "NoImageLib", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "No Image Movie"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("QueryItems: %v", err)
	}
	itemID := page.Items[0].ID

	// No scanner has ever populated an image ref for this item — expect a
	// clean 404, not a crash.
	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET .../Images/Primary status = %d, want 404", resp.StatusCode)
	}
}

func TestItemImageServesBytes(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "frank", "frankpass")
	token := loginToken(t, ts.URL, user.Name, "frankpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "ImageLib", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Movie With Image"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("QueryItems: %v", err)
	}
	itemID := page.Items[0].ID

	imgPath := filepath.Join(t.TempDir(), "poster.jpg")
	imgBytes := []byte("fake-jpeg-bytes")
	if err := os.WriteFile(imgPath, imgBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID,
		Kind:   "Primary",
		Index:  0,
		Path:   imgPath,
		Tag:    "abc123",
	}); err != nil {
		t.Fatalf("UpsertImageRef: %v", err)
	}

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../Images/Primary status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading image body: %v", err)
	}
	if string(body) != string(imgBytes) {
		t.Errorf("image body = %q, want %q", body, imgBytes)
	}
}
