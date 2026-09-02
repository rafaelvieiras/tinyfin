package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestUserPlayedItemsMarkResetsPosition regression-guards a real bug found
// via curl against a real Jellyfin server (staging, :8097): POST
// /UserPlayedItems/{itemId} — the SDK-shaped path the native player calls
// once it reaches the end of an item (see this file's package doc comment)
// — resets PlaybackPositionTicks to 0 on the unplayed -> played transition,
// same as its jellyfin-web-facing sibling route
// (userdata.go's handlePlayedItemAdd, see
// TestPlayedItemResetsResumePosition in userdata_test.go for the same fix
// there). Before this fix, a nonzero position reported via
// Sessions/Playing/Progress survived a mark-played call here too, and could
// resurface the item in "Continue Watching" once later unmarked.
func TestUserPlayedItemsMarkResetsPosition(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "gabe", "gabepass")
	token := loginToken(t, ts.URL, user.Name, "gabepass")
	get := authedClient(t, ts.URL, token)

	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Native Player Movie", Path: "/media/movies/native.mp4"}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Native Player Movie")

	if err := st.SetPlaystate(context.Background(), user.ID, itemID, storage.Playstate{PlaybackPositionTicks: 7_000_000_000}); err != nil {
		t.Fatalf("SetPlaystate: %v", err)
	}

	resp := get(http.MethodPost, "/UserPlayedItems/"+string(itemID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /UserPlayedItems/{itemId} status = %d, want 200", resp.StatusCode)
	}
	var result userItemDataDto
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !result.Played || result.PlayCount != 1 {
		t.Errorf("response = %+v, want Played=true PlayCount=1", result)
	}
	if result.PlaybackPositionTicks != 0 {
		t.Errorf("response PlaybackPositionTicks = %d, want 0", result.PlaybackPositionTicks)
	}

	state, err := st.GetPlaystate(context.Background(), user.ID, itemID)
	if err != nil {
		t.Fatalf("GetPlaystate: %v", err)
	}
	if state.PlaybackPositionTicks != 0 {
		t.Errorf("GetPlaystate after mark-played: PlaybackPositionTicks = %d, want 0", state.PlaybackPositionTicks)
	}
}
