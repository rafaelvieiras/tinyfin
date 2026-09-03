package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestItemRefreshLibrary covers the "Rastrear biblioteca" dashboard action:
// POST /Items/{itemId}/Refresh with a library's own ID queues a background
// scan (same mechanism as POST /Library/Refresh) and returns 204.
func TestItemRefreshLibrary(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	resp := get(http.MethodPost, "/Items/"+string(libID)+"/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&ReplaceAllImages=false&RegenerateTrickplay=false&ReplaceAllMetadata=false")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /Items/{libraryId}/Refresh status = %d, want 204", resp.StatusCode)
	}

	// runScan is queued in a goroutine (see item_refresh.go/library_admin.go)
	// — give it a moment, then confirm a scan actually ran rather than just
	// asserting the response code. ScanLibrary on an empty/nonexistent path
	// still completes (0 items), so this only checks the library itself is
	// still intact and the handler didn't block on the scan.
	time.Sleep(50 * time.Millisecond)
	if _, err := st.GetLibrary(context.Background(), libID); err != nil {
		t.Fatalf("GetLibrary after refresh: %v", err)
	}
}

// TestItemRefreshItemIsHonestNoOp covers the single-item form: an existing
// catalog item's ID returns 204 (the request is acknowledged) without
// triggering any real metadata re-fetch — this project has no such mechanism
// yet (item_refresh.go's doc comment). Regression guard: the item's stored
// fields must be untouched by the call.
func TestItemRefreshItemIsHonestNoOp(t *testing.T) {
	ts, st := newTestServer(t)
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

	resp := get(http.MethodPost, "/Items/"+string(itemID)+"/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&ReplaceAllImages=false&RegenerateTrickplay=false&ReplaceAllMetadata=false")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /Items/{itemId}/Refresh status = %d, want 204", resp.StatusCode)
	}

	after, err := st.GetItem(context.Background(), itemID)
	if err != nil {
		t.Fatalf("GetItem after refresh: %v", err)
	}
	if after.Name != "Refresh Movie" {
		t.Errorf("item.Name after refresh = %q, want unchanged %q (no-op, no real refresh mechanism exists)", after.Name, "Refresh Movie")
	}
}

// TestItemRefreshUnknownID404s covers an itemId that matches neither a
// library nor a catalog item.
func TestItemRefreshUnknownID404s(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)

	resp := get(http.MethodPost, "/Items/does-not-exist/Refresh")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /Items/{unknownId}/Refresh status = %d, want 404", resp.StatusCode)
	}
}

// TestItemRefreshRequiresAdmin covers the auth gate: no session at all gets
// 401, and an authenticated but non-admin user gets 403 — same pattern
// requireAdmin's own doc comment (auth.go) describes for library_admin.go's
// sibling routes.
func TestItemRefreshRequiresAdmin(t *testing.T) {
	ts, st := newTestServer(t)
	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	// No auth at all.
	noAuthResp, err := http.Post(ts.URL+"/Items/"+string(libID)+"/Refresh", "", nil)
	if err != nil {
		t.Fatalf("POST /Items/{itemId}/Refresh (no auth): %v", err)
	}
	defer noAuthResp.Body.Close()
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", noAuthResp.StatusCode)
	}

	// Authenticated, but not an administrator.
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	nonAdminResp := get(http.MethodPost, "/Items/"+string(libID)+"/Refresh")
	defer nonAdminResp.Body.Close()
	if nonAdminResp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", nonAdminResp.StatusCode)
	}
}
