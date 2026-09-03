// userdata.go implements the legacy per-user item-data routes jellyfin-web's
// bundled client actually calls for the favorite/watched toggle buttons on
// an item's detail page:
//
//	POST/DELETE /Users/{userId}/FavoriteItems/{itemId}
//	POST/DELETE /Users/{userId}/PlayedItems/{itemId}
//
// Confirmed live via Playwright against the jellyfin-web bundle this
// project ships (docs/11-jellyfin-web-route-coverage.md, "Users, sessions,
// devices" section): both buttons respond and even update optimistically in
// the UI, but with these routes unregistered the server 404s and nothing is
// ever persisted — the same silent-breakage pattern
// sessions_playback.go's package doc comment describes for the native
// player's playstate calls. Notably, POST /UserPlayedItems/{itemId}
// (sessions_playback.go's handleUserPlayedItemsMark) already implements the
// "mark played" persistence correctly — it's simply the wrong path for this
// button, which is why that route alone didn't fix it.
//
// {userId} is accepted but ignored, same as handleItemGet's
// /Users/{userId}/Items/{itemId} form (library.go) and every other
// per-user route in this project: there is no permissions model yet, so
// every handler operates on the authenticated caller
// (AuthFromContext(ctx).User.ID), not the path segment. Known
// simplification, not an oversight — documented at each call site that
// makes it, per this project's convention.
package api

import (
	"net/http"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// getPlaystateOrZero reads (userID, itemID)'s Playstate, treating a lookup
// error (no row yet — the common case for an item never touched before) as
// the zero value. Same convention as sessions_playback.go's
// reportPlaybackPosition/handleUserPlayedItemsMark and dto.go's
// userDataFor.
func (s *Server) getPlaystateOrZero(r *http.Request, userID storage.UserID, itemID storage.ItemID) storage.Playstate {
	state, err := s.store.GetPlaystate(r.Context(), userID, itemID)
	if err != nil {
		return storage.Playstate{}
	}
	return state
}

// writePlaystateUserData persists state for (userID, itemID) and, on
// success, writes the resulting userItemDataDto as the response body —
// the shape both FavoriteItems and PlayedItems (POST and DELETE alike)
// share with handleUserPlayedItemsMark's response.
func (s *Server) writePlaystateUserData(w http.ResponseWriter, r *http.Request, userID storage.UserID, itemID storage.ItemID, state storage.Playstate) {
	if err := s.store.SetPlaystate(r.Context(), userID, itemID, state); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, userItemDataDto{
		PlaybackPositionTicks: state.PlaybackPositionTicks,
		PlayCount:             state.PlayCount,
		IsFavorite:            state.IsFavorite,
		Played:                state.Played,
		Key:                   string(itemID),
		ItemID:                string(itemID),
	})
}

// handleFavoriteItemAdd is POST /Users/{userId}/FavoriteItems/{itemId}.
func (s *Server) handleFavoriteItemAdd(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state := s.getPlaystateOrZero(r, ac.User.ID, itemID)
	state.IsFavorite = true
	s.writePlaystateUserData(w, r, ac.User.ID, itemID, state)
}

// handleFavoriteItemRemove is DELETE /Users/{userId}/FavoriteItems/{itemId}.
func (s *Server) handleFavoriteItemRemove(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state := s.getPlaystateOrZero(r, ac.User.ID, itemID)
	state.IsFavorite = false
	s.writePlaystateUserData(w, r, ac.User.ID, itemID, state)
}

// handlePlayedItemAdd is POST /Users/{userId}/PlayedItems/{itemId} — the
// legacy path jellyfin-web's "mark watched" button actually calls (see this
// file's package doc comment).
//
// PlayCount only increments on the actual unplayed -> played transition,
// not on every call — confirmed against a real Jellyfin server (staging,
// :8097) by POSTing this route three times in a row against the same item:
// PlayCount went 0 -> 1 on the first call and then stayed at 1 for the
// second and third, only moving again after an intervening DELETE reset it
// to 0. This is a deliberate divergence from
// sessions_playback.go's handleUserPlayedItemsMark (POST
// /UserPlayedItems/{itemId}, the newer/SDK-shaped path), which increments
// unconditionally on every call — that route is only ever called once per
// playback (at end-of-video) by the clients that use it, so the difference
// has never been observable there, but this route is reachable from a
// UI button a user can click repeatedly on an already-watched item, where
// unconditional incrementing would be visibly wrong against the reference
// server's behavior.
//
// PlaybackPositionTicks resets to 0 here too — also confirmed against the
// real server, not assumed: reporting a nonzero position via
// Sessions/Playing/Progress and then marking the item played through this
// route comes back with position 0, not the stale reported value. Found to
// matter beyond this route's own response by testing the home screen's
// "Continue Watching" row end to end: since storage.ItemFilter.Resume
// (sqlite/item.go) only excludes an item while Played stays true, an
// item marked watched with its position left non-zero came back into the
// Resume row, mid-progress, the moment a later DELETE
// /Users/{userId}/PlayedItems/{itemId} flipped Played back to false — a
// real, reachable regression via the exact "mark watched" then "mark
// unwatched" button sequence jellyfin-web's item cards expose, not a
// hypothetical.
func (s *Server) handlePlayedItemAdd(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state := s.getPlaystateOrZero(r, ac.User.ID, itemID)
	if !state.Played {
		state.PlayCount++
	}
	state.Played = true
	state.PlaybackPositionTicks = 0
	// LastPlayedDate stamped to now — confirmed against a real Jellyfin
	// server (staging, :8097): the response to this exact route carries a
	// fresh LastPlayedDate. Same fix as sessions_playback.go's
	// handleUserPlayedItemsMark/reportPlaybackPosition (the native-player
	// equivalents of this jellyfin-web-facing route) and load-bearing for
	// the same reason: GET /Shows/NextUp (nextup.go) depends on it to
	// order/filter series by recency.
	now := time.Now().UTC()
	state.LastPlayedDate = &now
	s.writePlaystateUserData(w, r, ac.User.ID, itemID, state)
}

// handlePlayedItemRemove is DELETE /Users/{userId}/PlayedItems/{itemId} —
// unmarks watched. PlayCount resets to 0, not left untouched — confirmed
// against a real Jellyfin server (staging, :8097): DELETE PlayedItems
// after a PlayCount of 1 came back with PlayCount 0, not 1. LastPlayedDate
// is cleared the same way — confirmed the same way: the response carries
// no LastPlayedDate at all after this call, not the prior value.
func (s *Server) handlePlayedItemRemove(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state := s.getPlaystateOrZero(r, ac.User.ID, itemID)
	state.Played = false
	state.PlayCount = 0
	state.LastPlayedDate = nil
	s.writePlaystateUserData(w, r, ac.User.ID, itemID, state)
}
