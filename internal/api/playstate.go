package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file implements the playstate area (docs/contract/specs/playstate.md):
//
//	POST   /UserPlayedItems/{itemId}            mark played   (mark-played.R1-R6)
//	DELETE /UserPlayedItems/{itemId}            mark unplayed (mark-unplayed.R1-R3)
//	POST   /UserFavoriteItems/{itemId}          favorite
//	DELETE /UserFavoriteItems/{itemId}          unfavorite
//	POST   /Users/{userId}/PlayedItems/{itemId} legacy-web-required alias
//	DELETE /Users/{userId}/PlayedItems/{itemId} legacy-web-required alias
//	POST   /Sessions/Playing{,/Progress,/Stopped}
//
// Playback thresholds (playstate.md "Conventions") are fixed defaults; no
// server configuration for them exists yet.
const (
	// minResumePercent / maxResumePercent bound the share of the runtime a
	// reported position must fall in to be kept as a resume position.
	minResumePercent = 5
	maxResumePercent = 90
	// minResumeDurationTicks: items shorter than 300 s never keep a resume
	// position; a position between the thresholds marks them played.
	minResumeDurationTicks = int64(300) * 10_000_000
	// finishedGuardTicks: a position within the last second of the runtime
	// counts as finished.
	finishedGuardTicks = int64(10_000_000)
)

// isMarkFolder reports whether a played mark on this item type is a state of
// the folder itself (playstate.folder.R1) rather than of a playable leaf.
func isMarkFolder(t storage.ItemType) bool {
	return t == storage.ItemTypeSeries || t == storage.ItemTypeSeason || t == storage.ItemTypeBoxSet
}

// applyFolderUserData completes a user data object for folder-shaped items:
// UnplayedItemCount and, for series and seasons, the played state derived
// from the leaves below (playstate.folder.R2-R5, items.userdata.R8).
func (s *Server) applyFolderUserData(ctx context.Context, userID storage.UserID, item storage.Item, dto *userItemDataDto) {
	if item.Type == storage.ItemTypePlaylist {
		s.playlistUserData(ctx, userID, item, dto)
		return
	}
	if !childCountItemTypes[item.Type] {
		return
	}
	if isMarkFolder(item.Type) {
		total, unplayed, err := s.store.FolderLeafCounts(ctx, userID, item.ID)
		if err != nil {
			return
		}
		dto.UnplayedItemCount = &unplayed
		if total > 0 {
			dto.Played = unplayed == 0
			pct := float64(total-unplayed) * 100 / float64(total)
			dto.PlayedPercentage = &pct
		}
		return
	}
	if n, err := s.unplayedItemCountFor(ctx, userID, item); err == nil {
		dto.UnplayedItemCount = n
	}
}

// parseDatePlayed parses the datePlayed query value: an ISO 8601 date-time
// with a Z or numeric offset, normalized to UTC (mark-played.R4).
func parseDatePlayed(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// handleUserPlayedItemsMark is POST /UserPlayedItems/{itemId}.
func (s *Server) handleUserPlayedItemsMark(w http.ResponseWriter, r *http.Request) {
	s.markPlayed(w, r, caseInsensitiveQuery(r).Get("userid"), true)
}

// handleUserPlayedItemsUnmark is DELETE /UserPlayedItems/{itemId}.
func (s *Server) handleUserPlayedItemsUnmark(w http.ResponseWriter, r *http.Request) {
	s.markPlayed(w, r, caseInsensitiveQuery(r).Get("userid"), false)
}

// handleLegacyPlayedItemAdd is POST /Users/{userId}/PlayedItems/{itemId}, a
// thin alias kept because the official web client still calls it
// (playstate.legacy.R1, R3, R4): identical to the modern route with the user
// id taken from the path.
func (s *Server) handleLegacyPlayedItemAdd(w http.ResponseWriter, r *http.Request) {
	s.markPlayed(w, r, r.PathValue("userId"), true)
}

// handleLegacyPlayedItemRemove is DELETE /Users/{userId}/PlayedItems/{itemId}
// (playstate.legacy.R2).
func (s *Server) handleLegacyPlayedItemRemove(w http.ResponseWriter, r *http.Request) {
	s.markPlayed(w, r, r.PathValue("userId"), false)
}

// markPlayed implements both directions of the played mark for the user
// named by rawUserID (empty: the caller).
func (s *Server) markPlayed(w http.ResponseWriter, r *http.Request, rawUserID string, played bool) {
	var date *time.Time
	if played {
		if raw := caseInsensitiveQuery(r).Get("dateplayed"); raw != "" {
			t, ok := parseDatePlayed(raw)
			if !ok {
				writeInvalidID(w, "datePlayed", caseInsensitiveQuery(r).Get("dateplayed"))
				return
			}
			date = &t
		}
	}
	target, item, ok := s.resolveUserItemFor(w, r, rawUserID)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if item.Type == storage.ItemTypeBoxSet {
		// A collection has no state of its own: the mark is written to
		// every leaf below it and the collection reports the derived view
		// (collections.userdata.R2).
		if !s.markCollectionLeaves(w, r, target.ID, item, played, date, now) {
			return
		}
		dto := userItemDataFromState(s.getPlaystateOrZero(r, target.ID, item.ID), item)
		s.applyFolderUserData(r.Context(), target.ID, item, dto)
		writeJSON(w, http.StatusOK, dto)
		return
	}
	state := s.getPlaystateOrZero(r, target.ID, item.ID)
	folder := isMarkFolder(item.Type)
	switch {
	case folder && played:
		// A folder mark never writes to the leaves and ignores datePlayed
		// (playstate.folder.R1, R4).
		state.Played = true
		state.FolderMarkedAt = now.UnixNano()
	case folder:
		state.Played = false
		state.PlayCount = 0
		state.LastPlayedDate = nil
		state.FolderMarkedAt = now.UnixNano()
	case played:
		state.Played = true
		state.PlaybackPositionTicks = 0
		if date != nil {
			state.PlayCount++
			state.LastPlayedDate = date
		} else {
			if state.PlayCount < 1 {
				state.PlayCount = 1
			}
			if state.LastPlayedDate == nil {
				state.LastPlayedDate = &now
			}
		}
	default:
		// Unmarking forgets the history but keeps favorite, rating and
		// likes (mark-unplayed.R1, R2).
		state.Played = false
		state.PlayCount = 0
		state.PlaybackPositionTicks = 0
		state.LastPlayedDate = nil
		state.Touch = true
	}
	s.writeUserData(w, r, target.ID, item, state)
}

// markCollectionLeaves applies a played / unplayed mark to every leaf below a
// collection, the way a mark on the leaf itself would. It reports false after
// writing a failure response.
func (s *Server) markCollectionLeaves(w http.ResponseWriter, r *http.Request, userID storage.UserID, box storage.Item, played bool, date *time.Time, now time.Time) bool {
	const page = 500
	for offset := 0; ; offset += page {
		res, err := s.store.QueryItems(r.Context(), storage.ItemFilter{
			ParentID: &box.ID, Recursive: true, ExcludeItemTypes: storage.FolderItemTypes,
			SortBy: []storage.SortField{{By: storage.SortByName}}, Limit: page, Offset: offset,
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return false
		}
		for _, leaf := range res.Items {
			state := s.getPlaystateOrZero(r, userID, leaf.ID)
			if played {
				state.Played = true
				state.PlaybackPositionTicks = 0
				if date != nil {
					state.PlayCount++
					state.LastPlayedDate = date
				} else {
					if state.PlayCount < 1 {
						state.PlayCount = 1
					}
					if state.LastPlayedDate == nil {
						n := now
						state.LastPlayedDate = &n
					}
				}
			} else {
				state.Played = false
				state.PlayCount = 0
				state.PlaybackPositionTicks = 0
				state.LastPlayedDate = nil
				state.Touch = true
			}
			if err := s.store.SetPlaystate(r.Context(), userID, leaf.ID, state); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return false
			}
		}
		if offset+page >= res.TotalCount {
			return true
		}
	}
}

// writeUserData stores state and answers with the resulting user data.
func (s *Server) writeUserData(w http.ResponseWriter, r *http.Request, userID storage.UserID, item storage.Item, state storage.Playstate) {
	if err := s.store.SetPlaystate(r.Context(), userID, item.ID, state); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	dto := userItemDataFromState(state, item)
	s.applyFolderUserData(r.Context(), userID, item, dto)
	writeJSON(w, http.StatusOK, dto)
}

// handleUserFavoriteItemAdd is POST /UserFavoriteItems/{itemId}.
func (s *Server) handleUserFavoriteItemAdd(w http.ResponseWriter, r *http.Request) {
	s.setFavorite(w, r, true)
}

// handleUserFavoriteItemRemove is DELETE /UserFavoriteItems/{itemId}.
func (s *Server) handleUserFavoriteItemRemove(w http.ResponseWriter, r *http.Request) {
	s.setFavorite(w, r, false)
}

func (s *Server) setFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	target, item, ok := s.resolveUserItem(w, r)
	if !ok {
		return
	}
	state := s.getPlaystateOrZero(r, target.ID, item.ID)
	state.IsFavorite = favorite
	s.writeUserData(w, r, target.ID, item, state)
}

// playbackReport is the body shared by the start, progress and stopped
// reports (PlaybackStartInfo, PlaybackProgressInfo, PlaybackStopInfo).
// Pointer members tell an omitted or null member from a zero value. Members
// that are accepted but have no effect are not declared, except Item and
// SessionId's siblings, which need no decoding at all.
type playbackReport struct {
	ItemID              string                    `json:"ItemId"`
	MediaSourceID       *string                   `json:"MediaSourceId"`
	PlaySessionID       *string                   `json:"PlaySessionId"`
	PositionTicks       *int64                    `json:"PositionTicks"`
	IsPaused            *bool                     `json:"IsPaused"`
	IsMuted             *bool                     `json:"IsMuted"`
	CanSeek             *bool                     `json:"CanSeek"`
	VolumeLevel         *int                      `json:"VolumeLevel"`
	AudioStreamIndex    *int                      `json:"AudioStreamIndex"`
	SubtitleStreamIndex *int                      `json:"SubtitleStreamIndex"`
	PlayMethod          *string                   `json:"PlayMethod"`
	RepeatMode          *string                   `json:"RepeatMode"`
	PlaybackOrder       *string                   `json:"PlaybackOrder"`
	LiveStreamID        *string                   `json:"LiveStreamId"`
	PlaylistItemID      *string                   `json:"PlaylistItemId"`
	NowPlayingQueue     *[]nowPlayingQueueItemDto `json:"NowPlayingQueue"`
	Failed              *bool                     `json:"Failed"`
	Brightness          json.RawMessage           `json:"Brightness"`
	AspectRatio         json.RawMessage           `json:"AspectRatio"`
	Item                json.RawMessage           `json:"Item"`
	SessionID           json.RawMessage           `json:"SessionId"`
	NextMediaType       json.RawMessage           `json:"NextMediaType"`
}

// canonicalEnum maps value onto the matching name of allowed (names match
// case-insensitively, like the reference's string enum parsing) and reports
// whether it is one of them.
func canonicalEnum(value string, allowed ...string) (string, bool) {
	for _, a := range allowed {
		if strings.EqualFold(value, a) {
			return a, true
		}
	}
	return "", false
}

// decodePlaybackReport reads and validates a report body. Malformed JSON, a
// non-object body, wrong member types, a malformed ItemId and unknown enum
// names are all 400 (start.R7, progress.R11, stopped.R11); it writes the
// response itself on failure.
func decodePlaybackReport(w http.ResponseWriter, r *http.Request) (playbackReport, bool) {
	var rep playbackReport
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 || json.Unmarshal(raw, &rep) != nil {
		writeInvalidBody(w)
		return rep, false
	}
	if rep.ItemID != "" {
		id, ok := normalizeGUID(rep.ItemID)
		if !ok {
			writeInvalidID(w, "ItemId", rep.ItemID)
			return rep, false
		}
		rep.ItemID = id
		if id == zeroGUID {
			rep.ItemID = ""
		}
	}
	if rep.MediaSourceID != nil {
		// Echoed back in session state: an id is written canonically.
		if n, ok := normalizeGUID(*rep.MediaSourceID); ok {
			rep.MediaSourceID = &n
		}
	}
	enums := []struct {
		field   **string
		allowed []string
	}{
		{&rep.PlayMethod, []string{"Transcode", "DirectStream", "DirectPlay"}},
		{&rep.RepeatMode, []string{"RepeatNone", "RepeatAll", "RepeatOne"}},
		{&rep.PlaybackOrder, []string{"Default", "Shuffle"}},
	}
	for _, e := range enums {
		if *e.field == nil {
			continue
		}
		name, ok := canonicalEnum(**e.field, e.allowed...)
		if !ok {
			writeValidationProblem(w, "$", "The value is not valid for the request.")
			return rep, false
		}
		*e.field = &name
	}
	return rep, true
}

// applyReportedPosition applies a reported position to a play state
// following the resume rules shared by progress and stopped reports
// (progress.R2-R5, stopped.R3-R4). It never touches PlayCount.
func applyReportedPosition(state *storage.Playstate, runtime, pos int64, now time.Time) {
	if pos < 0 {
		// progress.R13: a negative position is stored as given and nothing
		// else changes (a stopped report with one never gets here).
		state.PlaybackPositionTicks = pos
		return
	}
	if runtime <= 0 {
		state.LastPlayedDate = &now
		state.PlaybackPositionTicks = pos
		return
	}
	pct := float64(pos) * 100 / float64(runtime)
	switch {
	case pct < minResumePercent:
		// Not far enough in: the saved position is cleared and the last
		// played date is left alone (progress.R12).
		state.PlaybackPositionTicks = 0
	case pct > maxResumePercent || pos >= runtime-finishedGuardTicks,
		runtime < minResumeDurationTicks:
		// Finished, or too short to ever keep a resume position.
		state.LastPlayedDate = &now
		state.Played = true
		state.PlaybackPositionTicks = 0
	default:
		state.LastPlayedDate = &now
		state.PlaybackPositionTicks = pos
	}
}

type reportKind int

const (
	reportStart reportKind = iota
	reportProgress
	reportStopped
)

// handlePlaybackReport serves the three playback report routes.
func (s *Server) handlePlaybackReport(w http.ResponseWriter, r *http.Request, kind reportKind) {
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rep, ok := decodePlaybackReport(w, r)
	if !ok {
		return
	}
	if kind == reportStopped && rep.PositionTicks != nil && *rep.PositionTicks < 0 {
		writeBadRequest(w)
		return
	}

	// An unknown or all-zero item is not an error and records nothing. An
	// item the caller cannot see is accepted too (start.R6, stopped.R8) and,
	// as on the reference, its user data IS written; the session never
	// shows it as now playing, so the report leaks nothing back.
	var item *storage.Item
	visible := false
	if rep.ItemID != "" {
		if it, err := s.store.GetItem(r.Context(), storage.ItemID(rep.ItemID)); err == nil {
			item = &it
			visible = s.itemReadableByCaller(r, it)
		}
	}

	now := time.Now().UTC()
	if item != nil {
		s.recordPlaybackUserData(r.Context(), ac.User.ID, *item, rep, kind, now)
		if kind != reportProgress {
			s.logPlaybackActivity(r, ac, *item, kind, now)
		}
	}
	if !visible {
		item = nil
	}
	s.updatePlaybackSession(r, ac, item, rep, kind, now)

	if kind == reportStopped && rep.ItemID != "" {
		s.transcode.Stop(rep.ItemID)
	}
	s.notifySessions(r.Context(), kind != reportProgress)
	w.WriteHeader(http.StatusNoContent)
}

// recordPlaybackUserData writes the user-data effects of a report.
func (s *Server) recordPlaybackUserData(ctx context.Context, userID storage.UserID, item storage.Item, rep playbackReport, kind reportKind, now time.Time) {
	state, err := s.store.GetPlaystate(ctx, userID, item.ID)
	if err != nil {
		state = storage.Playstate{}
	}
	var runtime int64
	if item.RunTimeTicks != nil {
		runtime = *item.RunTimeTicks
	}
	switch kind {
	case reportStart:
		// Each start counts a play; the position is not persisted
		// (start.R2-R4). A re-watch keeps its last played date.
		state.PlayCount++
		if !state.Played {
			state.LastPlayedDate = &now
		}
	case reportProgress:
		if rep.PositionTicks == nil {
			return
		}
		applyReportedPosition(&state, runtime, *rep.PositionTicks, now)
	case reportStopped:
		if rep.Failed != nil && *rep.Failed {
			return
		}
		if rep.PositionTicks == nil {
			// No position means watched to the end.
			state.PlayCount++
			state.Played = true
			state.PlaybackPositionTicks = 0
			state.LastPlayedDate = &now
		} else {
			applyReportedPosition(&state, runtime, *rep.PositionTicks, now)
		}
	}
	_ = s.store.SetPlaystate(ctx, userID, item.ID, state)
}

// updatePlaybackSession mirrors a report on the caller's session
// (playstate.session.R1-R8).
func (s *Server) updatePlaybackSession(r *http.Request, ac *AuthContext, item *storage.Item, rep playbackReport, kind reportKind, now time.Time) {
	var nowPlaying *baseItemDto
	if item != nil && kind != reportStopped {
		dto := s.baseItemDto(r.Context(), *item)
		nowPlaying = &dto
	}
	boolOr := func(p *bool) bool { return p != nil && *p }
	strOr := func(p *string, def string) string {
		if p != nil && *p != "" {
			return *p
		}
		return def
	}

	s.sessions.reportPlayback(ac.DeviceID, string(ac.User.ID), ac.User.Name, ac.Client, ac.Device, ac.Version, remoteEndPoint(r), func(ts *trackedSession) {
		if kind == reportStopped {
			ts.playing = nil
			if rep.NowPlayingQueue != nil {
				ts.queue = *rep.NowPlayingQueue
			}
			return
		}
		if item == nil {
			// Nothing playable to show; keep whatever state exists.
			return
		}
		method := strOr(rep.PlayMethod, "Transcode")
		if method == "Transcode" {
			// Without an active transcode job for the item the report is
			// recorded as direct play (session.R3).
			if _, running := s.transcode.Get(string(item.ID)); !running {
				method = "DirectPlay"
			}
		}
		ps := sessionPlayStateDto{
			PositionTicks:       rep.PositionTicks,
			CanSeek:             boolOr(rep.CanSeek),
			IsPaused:            boolOr(rep.IsPaused),
			IsMuted:             boolOr(rep.IsMuted),
			VolumeLevel:         rep.VolumeLevel,
			AudioStreamIndex:    rep.AudioStreamIndex,
			SubtitleStreamIndex: rep.SubtitleStreamIndex,
			MediaSourceID:       strOr(rep.MediaSourceID, string(item.ID)),
			PlayMethod:          method,
			RepeatMode:          strOr(rep.RepeatMode, "RepeatNone"),
			PlaybackOrder:       strOr(rep.PlaybackOrder, "Default"),
			LiveStreamID:        strOr(rep.LiveStreamID, ""),
		}
		var runtime int64
		if item.RunTimeTicks != nil {
			runtime = *item.RunTimeTicks
		}
		ts.playing = &playingState{item: nowPlaying, state: ps, runtime: runtime, reportedAt: now}
	})
}

// logPlaybackActivity appends the activity-log entry of a start or stop
// report (playstate.start.R10): VideoPlayback / AudioPlayback / Playback on
// a start, VideoPlaybackStopped / AudioPlaybackStopped on a stop and nothing
// for a stop of another media type. Every start and every stop writes one,
// a failed stop included, whatever the item's visibility to the caller.
// A failure to log never fails the report.
func (s *Server) logPlaybackActivity(r *http.Request, ac *AuthContext, item storage.Item, kind reportKind, now time.Time) {
	var typ storage.ActivityType
	var verb string
	switch item.Type {
	case storage.ItemTypeAudio:
		typ, verb = storage.ActivityTypeAudioPlayback, "is playing"
		if kind == reportStopped {
			typ, verb = storage.ActivityTypeAudioPlaybackStopped, "has finished playing"
		}
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeTrailer, storage.ItemTypeVideo:
		typ, verb = storage.ActivityTypeVideoPlayback, "is playing"
		if kind == reportStopped {
			typ, verb = storage.ActivityTypeVideoPlaybackStopped, "has finished playing"
		}
	default:
		if kind == reportStopped {
			return
		}
		typ, verb = storage.ActivityTypePlayback, "is playing"
	}
	uid := ac.User.ID
	iid := item.ID
	entry := storage.ActivityEntry{
		Name:     fmt.Sprintf("%s %s %s on %s", ac.User.Name, verb, item.Name, ac.Device),
		Type:     typ,
		Date:     now,
		UserID:   &uid,
		ItemID:   &iid,
		Severity: storage.ActivitySeverityInfo,
	}
	if _, err := s.store.AppendActivityEntry(r.Context(), entry); err != nil {
		log.Printf("api: appending playback activity log entry: %v", err)
		return
	}
	s.notifyActivityLog(r.Context())
}
