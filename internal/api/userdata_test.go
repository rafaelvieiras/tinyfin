package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestFavoriteItemsToggle regression-guards the "Adicionar aos favoritos"
// button found silently broken via a live Playwright session against
// jellyfin-web (docs/11-jellyfin-web-route-coverage.md, "Users, sessions,
// devices" section): the button responded and even updated optimistically,
// but POST/DELETE /Users/{userId}/FavoriteItems/{itemId} 404'd, so nothing
// was ever persisted.
func TestFavoriteItemsToggle(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Test Movie", Path: "/media/movies/test.mp4"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Test Movie")

	// POST marks favorite.
	addResp := get(http.MethodPost, "/Users/"+string(user.ID)+"/FavoriteItems/"+string(itemID))
	defer addResp.Body.Close()
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("POST FavoriteItems status = %d, want 200", addResp.StatusCode)
	}
	var addResult userItemDataDto
	if err := json.NewDecoder(addResp.Body).Decode(&addResult); err != nil {
		t.Fatalf("decoding POST FavoriteItems response: %v", err)
	}
	if !addResult.IsFavorite {
		t.Error("POST FavoriteItems response: IsFavorite = false, want true")
	}

	state, err := st.GetPlaystate(context.Background(), user.ID, itemID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if !state.IsFavorite {
		t.Error("GetPlaystate after POST FavoriteItems: IsFavorite = false, want true")
	}

	// GET /Items/{itemId} reflects it in UserData.
	detailResp := get(http.MethodGet, "/Items/"+string(itemID))
	defer detailResp.Body.Close()
	var detail baseItemDto
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding item detail: %v", err)
	}
	if detail.UserData == nil || !detail.UserData.IsFavorite {
		t.Errorf("GET /Items/{itemId} UserData = %+v, want IsFavorite = true", detail.UserData)
	}

	// DELETE unmarks favorite.
	delResp := get(http.MethodDelete, "/Users/"+string(user.ID)+"/FavoriteItems/"+string(itemID))
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE FavoriteItems status = %d, want 200", delResp.StatusCode)
	}
	var delResult userItemDataDto
	if err := json.NewDecoder(delResp.Body).Decode(&delResult); err != nil {
		t.Fatalf("decoding DELETE FavoriteItems response: %v", err)
	}
	if delResult.IsFavorite {
		t.Error("DELETE FavoriteItems response: IsFavorite = true, want false")
	}

	state2, err := st.GetPlaystate(context.Background(), user.ID, itemID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if state2.IsFavorite {
		t.Error("GetPlaystate after DELETE FavoriteItems: IsFavorite = true, want false")
	}
}

// TestPlayedItemsToggle regression-guards the "Marcar como reproduzido"
// button, broken the same way as TestFavoriteItemsToggle above but via
// POST/DELETE /Users/{userId}/PlayedItems/{itemId} — the legacy path
// jellyfin-web's bundled client actually calls, distinct from the
// newer/SDK-shaped POST /UserPlayedItems/{itemId}
// (sessions_playback.go's handleUserPlayedItemsMark) which was already
// correctly implemented but unreachable from this button.
func TestPlayedItemsToggle(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "erin", "erinpass")
	token := loginToken(t, ts.URL, user.Name, "erinpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Test Movie", Path: "/media/movies/test2.mp4"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Test Movie")

	// POST marks played and increments PlayCount.
	addResp := get(http.MethodPost, "/Users/"+string(user.ID)+"/PlayedItems/"+string(itemID))
	defer addResp.Body.Close()
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("POST PlayedItems status = %d, want 200", addResp.StatusCode)
	}
	var addResult userItemDataDto
	if err := json.NewDecoder(addResp.Body).Decode(&addResult); err != nil {
		t.Fatalf("decoding POST PlayedItems response: %v", err)
	}
	if !addResult.Played {
		t.Error("POST PlayedItems response: Played = false, want true")
	}
	if addResult.PlayCount != 1 {
		t.Errorf("POST PlayedItems response: PlayCount = %d, want 1", addResult.PlayCount)
	}

	state, err := st.GetPlaystate(context.Background(), user.ID, itemID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if !state.Played || state.PlayCount != 1 {
		t.Errorf("GetPlaystate after POST PlayedItems: Played=%v PlayCount=%d, want true/1", state.Played, state.PlayCount)
	}

	// A second POST on an already-played item does NOT increment PlayCount
	// again — confirmed against a real Jellyfin server (see userdata.go's
	// handlePlayedItemAdd doc comment): PlayCount only advances on the
	// unplayed -> played transition.
	addResp2 := get(http.MethodPost, "/Users/"+string(user.ID)+"/PlayedItems/"+string(itemID))
	defer addResp2.Body.Close()
	var addResult2 userItemDataDto
	if err := json.NewDecoder(addResp2.Body).Decode(&addResult2); err != nil {
		t.Fatalf("decoding second POST PlayedItems response: %v", err)
	}
	if addResult2.PlayCount != 1 {
		t.Errorf("second POST PlayedItems response: PlayCount = %d, want 1 (unchanged)", addResult2.PlayCount)
	}

	// GET /Items/{itemId} reflects it in UserData.
	detailResp := get(http.MethodGet, "/Items/"+string(itemID))
	defer detailResp.Body.Close()
	var detail baseItemDto
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding item detail: %v", err)
	}
	if detail.UserData == nil || !detail.UserData.Played {
		t.Errorf("GET /Items/{itemId} UserData = %+v, want Played = true", detail.UserData)
	}

	// DELETE unmarks played and resets PlayCount to 0 — confirmed against a
	// real Jellyfin server (see userdata.go's handlePlayedItemRemove doc
	// comment): PlayCount is not left at its prior value.
	delResp := get(http.MethodDelete, "/Users/"+string(user.ID)+"/PlayedItems/"+string(itemID))
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE PlayedItems status = %d, want 200", delResp.StatusCode)
	}
	var delResult userItemDataDto
	if err := json.NewDecoder(delResp.Body).Decode(&delResult); err != nil {
		t.Fatalf("decoding DELETE PlayedItems response: %v", err)
	}
	if delResult.Played {
		t.Error("DELETE PlayedItems response: Played = true, want false")
	}
	if delResult.PlayCount != 0 {
		t.Errorf("DELETE PlayedItems response: PlayCount = %d, want 0", delResult.PlayCount)
	}

	state2, err := st.GetPlaystate(context.Background(), user.ID, itemID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if state2.Played {
		t.Error("GetPlaystate after DELETE PlayedItems: Played = true, want false")
	}
	if state2.PlayCount != 0 {
		t.Errorf("GetPlaystate after DELETE PlayedItems: PlayCount = %d, want 0", state2.PlayCount)
	}
}

// TestPlayedItemResetsResumePosition regression-guards a real bug found via
// curl against a real Jellyfin server (staging, :8097): marking an
// in-progress item watched resets PlaybackPositionTicks to 0, not just
// Played/PlayCount. Before this fix, handlePlayedItemAdd left the position
// untouched — invisible in the mark-played response itself (Resume already
// excludes any Played item, position or not — storage.ItemFilter.Resume,
// sqlite/item.go), but it resurfaced the moment the item was later
// unmarked watched: DELETE PlayedItems flips Played back to false without
// touching position (correctly — see TestPlayedItemsToggle above), so a
// stale nonzero position left over from before the mark-played call put
// the item right back in the "Continue Watching" row, mid-progress, via
// exactly the "mark watched" then "mark unwatched" button sequence
// jellyfin-web's item cards expose.
func TestPlayedItemResetsResumePosition(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "frank", "frankpass")
	token := loginToken(t, ts.URL, user.Name, "frankpass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "In Progress Movie", Path: "/media/movies/inprogress.mp4"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "In Progress Movie")

	// Simulate a real in-progress watch, same as Sessions/Playing/Progress
	// would persist.
	if err := st.SetPlaystate(context.Background(), user.ID, itemID, storage.Playstate{PlaybackPositionTicks: 5_000_000_000}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}

	addResp := get(http.MethodPost, "/Users/"+string(user.ID)+"/PlayedItems/"+string(itemID))
	defer addResp.Body.Close()
	var addResult userItemDataDto
	if err := json.NewDecoder(addResp.Body).Decode(&addResult); err != nil {
		t.Fatalf("decoding POST PlayedItems response: %v", err)
	}
	if addResult.PlaybackPositionTicks != 0 {
		t.Errorf("POST PlayedItems response: PlaybackPositionTicks = %d, want 0", addResult.PlaybackPositionTicks)
	}

	delResp := get(http.MethodDelete, "/Users/"+string(user.ID)+"/PlayedItems/"+string(itemID))
	defer delResp.Body.Close()
	var delResult userItemDataDto
	if err := json.NewDecoder(delResp.Body).Decode(&delResult); err != nil {
		t.Fatalf("decoding DELETE PlayedItems response: %v", err)
	}
	if delResult.PlaybackPositionTicks != 0 {
		t.Errorf("DELETE PlayedItems response: PlaybackPositionTicks = %d, want 0", delResult.PlaybackPositionTicks)
	}

	// The item must not resurface in Continue Watching after this cycle.
	resumeResp := get(http.MethodGet, "/UserItems/Resume?mediaTypes=Video")
	defer resumeResp.Body.Close()
	var resumeResult baseItemDtoQueryResult
	if err := json.NewDecoder(resumeResp.Body).Decode(&resumeResult); err != nil {
		t.Fatalf("decoding Resume response: %v", err)
	}
	for _, it := range resumeResult.Items {
		if it.ID == string(itemID) {
			t.Errorf("UserItems/Resume includes %q after mark-played/unmark-played cycle, want excluded", it.Name)
		}
	}
}
