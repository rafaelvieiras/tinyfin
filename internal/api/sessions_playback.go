// sessions_playback.go implements the playstate-reporting routes the
// native jellyfin-android player calls around actual playback (start,
// periodic progress, stop, mark-watched) plus GET /MediaSegments/{itemId}
// (intro/outro/commercial-skip markers). playback.go's package doc comment
// used to list "playstate reporting" as out of scope for this slice —
// found to be a mistake, not a deliberate deferral, while mapping the
// whole native playback flow for coverage: jellyfin-sdk-kotlin's
// RawResponse.createContent (see users.go's userPolicyDto doc comment)
// catches a 404 on these the same way it catches a deserialization
// failure, so leaving them unregistered doesn't crash the app, it just
// silently breaks resume position and watched-state — the same class of
// invisible regression Sessions/Capabilities/Full turned out to be before
// system.go's handleSessionsCapabilitiesFull was added.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// playbackReportBody covers the fields this project actually consumes
// across PlaybackStartInfo/PlaybackProgressInfo/PlaybackStopInfo — all
// three OpenAPI schemas share this shape, and every other field on them
// (audio/subtitle stream index, bitrate, IsPaused, ...) is session/
// transcode-negotiation detail this slice doesn't track.
type playbackReportBody struct {
	ItemID        string `json:"ItemId"`
	PositionTicks *int64 `json:"PositionTicks"`
}

// handleSessionsPlaying is POST /Sessions/Playing — called once when
// playback actually starts. There is no session/realtime-state store yet
// (docs/06 is out of scope here, same reasoning as
// handleSessionsCapabilitiesFull's doc comment in system.go), and the
// resume position at start is whatever PlaybackInfo already told the
// client, so this is a pure fire-and-forget acknowledgement.
func (s *Server) handleSessionsPlaying(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionsPlayingProgress is POST /Sessions/Playing/Progress —
// called periodically while playback runs. Unlike handleSessionsPlaying,
// this one does have somewhere durable to put its data:
// storage.PlaystateStore already exists (for GetItem's future per-user
// aggregates), so the resume position is actually persisted here rather
// than discarded, which is what makes "continue watching" work at all.
// Played/PlayCount/IsFavorite are read back first and preserved
// unchanged — this route only ever reports position, never watched
// status (that's UserPlayedItems, below).
func (s *Server) handleSessionsPlayingProgress(w http.ResponseWriter, r *http.Request) {
	s.reportPlaybackPosition(r)
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionsPlayingStopped is POST /Sessions/Playing/Stopped — called
// once when playback ends or is abandoned, reporting the final position.
// Same persistence as handleSessionsPlayingProgress; determining whether
// the item counts as "watched" is deliberately not attempted here (no
// completion-threshold config exists) — the native client itself calls
// POST /UserPlayedItems/{itemId} separately when it reaches the end of
// the item, which is what actually marks it played.
func (s *Server) handleSessionsPlayingStopped(w http.ResponseWriter, r *http.Request) {
	s.reportPlaybackPosition(r)
	w.WriteHeader(http.StatusNoContent)
}

// reportPlaybackPosition decodes a playbackReportBody and, given a valid
// ItemId and an authenticated user, updates just the PlaybackPositionTicks
// field of that (user, item) pair's Playstate. Errors are swallowed
// throughout (decode failure, missing auth, store failure) — these are
// fire-and-forget reporting calls from the client's point of view on the
// reference server too; failing the HTTP response for a malformed or
// racing report would be a worse experience than silently skipping the
// position update.
func (s *Server) reportPlaybackPosition(r *http.Request) {
	var body playbackReportBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ItemID == "" || body.PositionTicks == nil {
		return
	}
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		return
	}

	itemID := storage.ItemID(body.ItemID)
	state, err := s.store.GetPlaystate(r.Context(), ac.User.ID, itemID)
	if err != nil {
		state = storage.Playstate{}
	}
	state.PlaybackPositionTicks = *body.PositionTicks
	_ = s.store.SetPlaystate(r.Context(), ac.User.ID, itemID, state)
}

// userItemDataDto mirrors the OpenAPI schema's UserItemDataDto, trimmed to
// the fields jellyfin-sdk-kotlin's UserItemDataDto.kt declares required
// (no default) — found the same way as mediaSourceInfoDto/mediaStreamDto's
// extra fields (reading the Kotlin source directly, not just testing)
// while mapping the whole native playback flow for coverage.
type userItemDataDto struct {
	PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
	PlayCount             int    `json:"PlayCount"`
	IsFavorite            bool   `json:"IsFavorite"`
	Played                bool   `json:"Played"`
	Key                   string `json:"Key"`
	ItemID                string `json:"ItemId"`
}

// handleUserPlayedItemsMark is POST /UserPlayedItems/{itemId} — called by
// the native player once when it reaches the end of an item, marking it
// watched. This is the actual "played" signal (see
// handleSessionsPlayingStopped's doc comment above for why that route
// doesn't try to infer it itself).
func (s *Server) handleUserPlayedItemsMark(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state, err := s.store.GetPlaystate(r.Context(), ac.User.ID, itemID)
	if err != nil {
		state = storage.Playstate{}
	}
	state.Played = true
	state.PlayCount++
	if err := s.store.SetPlaystate(r.Context(), ac.User.ID, itemID, state); err != nil {
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

// mediaSegmentDtoQueryResult mirrors the OpenAPI schema's
// MediaSegmentDtoQueryResult — same envelope shape as
// baseItemDtoQueryResult (dto.go), kept as its own type since its Items
// element type differs.
type mediaSegmentDtoQueryResult struct {
	Items            []struct{} `json:"Items"`
	TotalRecordCount int        `json:"TotalRecordCount"`
	StartIndex       int        `json:"StartIndex"`
}

// handleMediaSegments is GET /MediaSegments/{itemId} — intro/outro/
// commercial-skip markers. Tinyfin has no segment-detection pipeline
// (that's a scanner/analysis feature, out of scope for this slice the
// same way trickplay generation is), so this always answers "no
// segments" rather than 404ing: jellyfin-android's
// MediaSegmentRepository wraps this whole call in runCatching{} anyway
// (found by reading its source), so a 404 wouldn't have broken anything
// either, but an honest empty result is cheap to give and avoids relying
// on that catch-all.
func (s *Server) handleMediaSegments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mediaSegmentDtoQueryResult{Items: []struct{}{}})
}
