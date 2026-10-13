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
	"time"

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
// playback actually starts. The resume position at start is whatever
// PlaybackInfo already told the client, so there's nothing to persist here
// — but sessions.go's registry does exist now, so this at least touches the
// caller's LastActivityDate if it already has a session registered (see
// touchSession below); it does not itself register one (that's
// system.go's handleSessionsCapabilitiesFull's job — this route runs after
// playback has already started, on a device that's expected to have called
// that first).
func (s *Server) handleSessionsPlaying(w http.ResponseWriter, r *http.Request) {
	s.touchSession(r, true)
	w.WriteHeader(http.StatusNoContent)
}

// touchSession stamps LastActivityDate to now on the caller's tracked
// session (sessions.go's sessionRegistry), keyed by the auth context's
// DeviceID — a no-op if there's no auth context or no session already
// registered for that device (sessionRegistry.touch's own doc comment).
// Shared by all three playback-report handlers below, which otherwise have
// no reason to read AuthContext directly (reportPlaybackPosition already
// does, for its own purposes, but doesn't expose it to its callers).
//
// force is passed straight through to notifySessions (websocket.go): a
// playback start/stop is a real transition (forced, sent immediately to
// every subscribed connection), a periodic progress report is throttled —
// the same force/throttle split docs/12 documents for the reference
// server's own equivalent listener.
func (s *Server) touchSession(r *http.Request, force bool) {
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		s.sessions.touch(ac.DeviceID)
		s.notifySessions(r.Context(), force)
	}
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
//
// LastPlayedDate is stamped to now on every report — confirmed against a
// real Jellyfin server (reference server): a Sessions/Playing/Progress call
// reporting a nonzero position came back (via the equivalent GetItem
// UserData) with a fresh LastPlayedDate, before the item was ever marked
// fully watched. GET /Shows/NextUp (nextup.go) depends on this being kept
// current — it's the only signal that lets a series with an in-progress,
// not-yet-finished episode be recognized as "touched" at all.
func (s *Server) handleSessionsPlayingProgress(w http.ResponseWriter, r *http.Request) {
	s.reportPlaybackPosition(r)
	s.touchSession(r, false)
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionsPlayingStopped is POST /Sessions/Playing/Stopped — called
// once when playback ends or is abandoned, reporting the final position.
// Same persistence as handleSessionsPlayingProgress; determining whether
// the item counts as "watched" is deliberately not attempted here (no
// completion-threshold config exists) — the native client itself calls
// POST /UserPlayedItems/{itemId} separately when it reaches the end of
// the item, which is what actually marks it played.
//
// Also the primary teardown path for a Phase 2 transcode job (hls.go's
// ensureTranscodeJob doc comment): a job's context is intentionally
// long-lived (context.Background()), not tied to any single HLS request,
// so nothing else in this project stops the ffmpeg subprocess and removes
// its output directory once a client is actually done with an item. A
// Direct Play item has no such job (s.transcode.Stop is a documented
// no-op for an unknown session ID), so this runs unconditionally rather
// than needing to know in advance which mode was in play.
func (s *Server) handleSessionsPlayingStopped(w http.ResponseWriter, r *http.Request) {
	itemID := s.reportPlaybackPosition(r)
	s.touchSession(r, true)
	if itemID != "" {
		s.transcode.Stop(string(itemID))
	}
	w.WriteHeader(http.StatusNoContent)
}

// reportPlaybackPosition decodes a playbackReportBody and, given a valid
// ItemId and an authenticated user, updates just the PlaybackPositionTicks
// field of that (user, item) pair's Playstate, returning the decoded
// ItemId (or "" on any failure) so callers that need it — currently only
// handleSessionsPlayingStopped, to key its transcode-job teardown — don't
// have to decode the body a second time. Errors are otherwise swallowed
// throughout (decode failure, missing auth, store failure) — these are
// fire-and-forget reporting calls from the client's point of view on the
// reference server too; failing the HTTP response for a malformed or
// racing report would be a worse experience than silently skipping the
// position update.
func (s *Server) reportPlaybackPosition(r *http.Request) storage.ItemID {
	var body playbackReportBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ItemID == "" || body.PositionTicks == nil {
		return ""
	}
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		return ""
	}

	itemID := storage.ItemID(body.ItemID)
	state, err := s.store.GetPlaystate(r.Context(), ac.User.ID, itemID)
	if err != nil {
		state = storage.Playstate{}
	}
	state.PlaybackPositionTicks = *body.PositionTicks
	now := time.Now().UTC()
	state.LastPlayedDate = &now
	_ = s.store.SetPlaystate(r.Context(), ac.User.ID, itemID, state)
	return itemID
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

	// UnplayedItemCount is only ever set for a folder-shaped item that
	// aggregates its children's watched status (Series/Season/BoxSet,
	// dto.go's childCountItemTypes) — nil for anything else, and for the
	// call sites in this file, which build a userItemDataDto directly
	// rather than through dto.go's userDataFor (playable, non-folder
	// items). See dto.go's unplayedItemCountFor and docs/12's
	// "Season/Series/BoxSet UserData — UnplayedItemCount".
	UnplayedItemCount *int `json:"UnplayedItemCount,omitempty"`
}

// handleUserPlayedItemsMark is POST /UserPlayedItems/{itemId} — called by
// the native player once when it reaches the end of an item, marking it
// watched. This is the actual "played" signal (see
// handleSessionsPlayingStopped's doc comment above for why that route
// doesn't try to infer it itself).
//
// PlaybackPositionTicks resets to 0 on the transition — confirmed against a
// real Jellyfin server (reference server): reporting a nonzero position via
// Sessions/Playing/Progress and then marking the item played through this
// route comes back with position 0. Same fix, same confirmation, as
// userdata.go's handlePlayedItemAdd (the jellyfin-web-facing sibling of
// this route) — left stale otherwise, a watched item could resurface
// mid-progress in the "Continue Watching" row (storage.ItemFilter.Resume,
// sqlite/item.go) the moment it's later unmarked watched.
//
// LastPlayedDate is stamped to now too — same confirmation as
// reportPlaybackPosition's doc comment above, and load-bearing for the
// same reason: GET /Shows/NextUp (nextup.go) orders series by it and can
// filter on nextUpDateCutoff, both of which depend on this being kept
// current rather than left nil forever.
func (s *Server) handleUserPlayedItemsMark(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	ac, _ := AuthFromContext(r.Context())

	state, err := s.store.GetPlaystate(r.Context(), ac.User.ID, itemID)
	if err != nil {
		state = storage.Playstate{}
	}
	state.Played = true
	state.PlayCount++
	state.PlaybackPositionTicks = 0
	now := time.Now().UTC()
	state.LastPlayedDate = &now
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
