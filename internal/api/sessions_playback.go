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
	"net/http"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// handleSessionsPlaying is POST /Sessions/Playing — the client announces
// that playback began (playstate.start.R1-R9).
func (s *Server) handleSessionsPlaying(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackReport(w, r, reportStart)
}

// handleSessionsPlayingProgress is POST /Sessions/Playing/Progress — the
// periodic report where the resume position, or the finished state, is
// saved (playstate.progress.R1-R11).
func (s *Server) handleSessionsPlayingProgress(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackReport(w, r, reportProgress)
}

// handleSessionsPlayingStopped is POST /Sessions/Playing/Stopped — the
// client ends playback (playstate.stopped.R1-R11). Also the primary teardown
// path for a Phase 2 transcode job (hls.go's ensureTranscodeJob doc
// comment): a job's context is intentionally long-lived, so nothing else
// stops the ffmpeg subprocess once a client is done with an item. A Direct
// Play item has no such job (s.transcode.Stop is a documented no-op for an
// unknown session ID).
func (s *Server) handleSessionsPlayingStopped(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackReport(w, r, reportStopped)
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

	// Optional members, present only when they have a value (set by the
	// UserItems/{itemId}/UserData routes, userdata_api.go).
	PlayedPercentage *float64  `json:"PlayedPercentage,omitempty"`
	Rating           *float64  `json:"Rating,omitempty"`
	Likes            *bool     `json:"Likes,omitempty"`
	LastPlayedDate   *wireTime `json:"LastPlayedDate,omitempty"`
}

// handleSessionsPlayingPing is POST /Sessions/Playing/Ping — keeps a
// transcode alive without progress. Tinyfin tracks no transcode by play
// session here, so it only validates the parameter (playstate.ping.R1-R3):
// 204 for a non-empty playSessionId, 400 otherwise, no state touched.
func (s *Server) handleSessionsPlayingPing(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("playSessionId") == "" {
		writeValidationProblem(w, "playSessionId", "The playSessionId field is required.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// handleMediaSegments is GET /MediaSegments/{itemId}: the intro/outro/
// commercial markers of an item. Tinyfin has no segment provider, so the
// answer is always an empty list (chapters are not turned into segments, as
// the reference does not either). The id and the includeSegmentTypes filter
// are validated like the reference does: a malformed id or type is a 400
// (checked before the lookup), an unknown item a 404.
func (s *Server) handleMediaSegments(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("itemId")
	norm, ok := normalizeGUID(rawID)
	if !ok || norm == zeroGUID {
		writeInvalidID(w, "itemId", rawID)
		return
	}
	for _, v := range r.URL.Query()["includeSegmentTypes"] {
		if msg := segmentTypesProblem(v); msg != "" {
			writeValidationProblem(w, "includeSegmentTypes", "The value '"+v+"' is "+msg+".")
			return
		}
	}
	ctx := r.Context()
	if _, err := s.store.GetItem(ctx, storage.ItemID(norm)); err != nil {
		if _, libErr := s.store.GetLibrary(ctx, storage.LibraryID(norm)); libErr != nil {
			writeItemsProblem(w, http.StatusNotFound, "Not Found")
			return
		}
	}
	writeJSON(w, http.StatusOK, mediaSegmentDtoQueryResult{Items: []struct{}{}})
}

// segmentTypesProblem validates one includeSegmentTypes value: a comma list
// of segment type names (any case) or numbers 0-5. It returns "" when valid,
// "invalid" for an empty value or a number out of range and "not valid" for
// anything else.
func segmentTypesProblem(v string) string {
	if v == "" {
		return "invalid"
	}
	problem := ""
	for _, tok := range strings.Split(v, ",") {
		tok = strings.TrimSpace(tok)
		switch strings.ToLower(tok) {
		case "unknown", "commercial", "preview", "recap", "outro", "intro":
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil {
			if n < 0 || n > 5 {
				return "invalid"
			}
			continue
		}
		problem = "not valid"
	}
	return problem
}
