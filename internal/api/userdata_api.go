package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file implements the modern per-user item data routes and the
// resume ("continue watching") list:
//
//	GET  /UserItems/{itemId}/UserData   (items.userdata.R1-R5)
//	POST /UserItems/{itemId}/UserData   (items.userdata.R6-R13)
//	GET  /UserItems/Resume              (items.resume.R1-R11)
//
// The legacy /Users/{userId}/Items/{itemId}/UserData and
// /Users/{userId}/Items/Resume forms are excluded from the contract
// (docs/contract/scope-excluded.md) and deliberately not routed.

// userDataBody is the partial-update body of POST /UserItems/{itemId}/UserData.
// A member that is absent or null is left unchanged; Key, ItemId,
// PlayedPercentage and UnplayedItemCount are accepted by the wire shape but
// ignored, so they are simply not declared here.
type userDataBody struct {
	Played                *bool        `json:"Played"`
	IsFavorite            *bool        `json:"IsFavorite"`
	PlaybackPositionTicks *int64       `json:"PlaybackPositionTicks"`
	PlayCount             *int         `json:"PlayCount"`
	LastPlayedDate        *requestTime `json:"LastPlayedDate"`
	Rating                *float64     `json:"Rating"`
	Likes                 *bool        `json:"Likes"`
}

// userItemDataFromState builds the user data object for item from a stored
// playstate. Optional members (Rating, Likes, LastPlayedDate,
// PlayedPercentage) appear only when they have a value.
func userItemDataFromState(state storage.Playstate, item storage.Item) *userItemDataDto {
	dto := &userItemDataDto{
		PlaybackPositionTicks: state.PlaybackPositionTicks,
		PlayCount:             state.PlayCount,
		IsFavorite:            state.IsFavorite,
		Played:                state.Played,
		Key:                   string(item.ID),
		ItemID:                string(item.ID),
		Rating:                state.Rating,
		Likes:                 state.Likes,
		LastPlayedDate:        wireTimePtr(state.LastPlayedDate),
	}
	if item.RunTimeTicks != nil && *item.RunTimeTicks > 0 && state.PlaybackPositionTicks > 0 {
		p := float64(state.PlaybackPositionTicks) * 100 / float64(*item.RunTimeTicks)
		dto.PlayedPercentage = &p
	}
	return dto
}

// resolveUserItem applies the shared item rules of both user data routes
// and resolves the target user: the path item id must be a GUID (400, and
// the all-zero id is invalid), the target user follows
// resolveUserTarget (403/404), and an unknown item or one outside the
// target's libraries is 404. It writes the failure response itself.
func (s *Server) resolveUserItem(w http.ResponseWriter, r *http.Request) (storage.User, storage.Item, bool) {
	return s.resolveUserItemFor(w, r, caseInsensitiveQuery(r).Get("userid"))
}

// resolveUserItemFor is resolveUserItem with the requested user id given
// explicitly (the legacy played aliases carry it in the path). A missing
// item is a problem-details 404.
func (s *Server) resolveUserItemFor(w http.ResponseWriter, r *http.Request, rawUserID string) (storage.User, storage.Item, bool) {
	raw := r.PathValue("itemId")
	id, ok := normalizeGUID(raw)
	if !ok || id == zeroGUID {
		writeInvalidID(w, "itemId", raw)
		return storage.User{}, storage.Item{}, false
	}
	target, ok := s.resolveUserTarget(w, r, rawUserID)
	if !ok {
		return storage.User{}, storage.Item{}, false
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(id))
	if err != nil {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return storage.User{}, storage.Item{}, false
	}
	// The target user's own scope applies (library, tags, parental rules).
	sc, err := s.itemsScopeFor(r.Context(), target)
	if err != nil || !s.itemReadable(r.Context(), sc, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return storage.User{}, storage.Item{}, false
	}
	return target, item, true
}

// handleUserItemDataGet is GET /UserItems/{itemId}/UserData.
func (s *Server) handleUserItemDataGet(w http.ResponseWriter, r *http.Request) {
	target, item, ok := s.resolveUserItem(w, r)
	if !ok {
		return
	}
	state := s.getPlaystateOrZero(r, target.ID, item.ID)
	dto := userItemDataFromState(state, item)
	s.applyFolderUserData(r.Context(), target.ID, item, dto)
	writeJSON(w, http.StatusOK, dto)
}

// handleUserItemDataPost is POST /UserItems/{itemId}/UserData: a partial
// update of the target user's data for one item. Played only sets the flag
// (items.userdata.R9) and a folder write never propagates to children
// (items.userdata.R13).
func (s *Server) handleUserItemDataPost(w http.ResponseWriter, r *http.Request) {
	target, item, ok := s.resolveUserItem(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw))[0] != '{' {
		writeInvalidBody(w)
		return
	}
	var body userDataBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeInvalidBody(w)
		return
	}

	state := s.getPlaystateOrZero(r, target.ID, item.ID)
	if body.Played != nil {
		state.Played = *body.Played
	}
	if body.IsFavorite != nil {
		state.IsFavorite = *body.IsFavorite
	}
	if body.PlaybackPositionTicks != nil {
		state.PlaybackPositionTicks = *body.PlaybackPositionTicks
	}
	if body.PlayCount != nil {
		state.PlayCount = *body.PlayCount
	}
	if body.LastPlayedDate != nil {
		t := body.LastPlayedDate.Time.UTC()
		state.LastPlayedDate = &t
	}
	if body.Rating != nil {
		state.Rating = body.Rating
	}
	if body.Likes != nil {
		state.Likes = body.Likes
	}
	if err := s.store.SetPlaystate(r.Context(), target.ID, item.ID, state); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	dto := userItemDataFromState(state, item)
	s.applyFolderUserData(r.Context(), target.ID, item, dto)
	writeJSON(w, http.StatusOK, dto)
}

// splitCSV splits a comma-separated query value, dropping blanks.
func splitCSV(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// handleUserItemsResume serves GET /UserItems/Resume — the "continue
// watching" list: items with a saved playback position for the target
// user, most recently played first (items.resume.R1-R11).
//
// ExcludeActiveSessions=true drops the items the user is playing right now
// in any tracked session; with nothing playing the list is unchanged
// (items.resume.R11).
func (s *Server) handleUserItemsResume(w http.ResponseWriter, r *http.Request) {
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := caseInsensitiveQuery(r)
	target, ok := s.resolveUserTarget(w, r, q.Get("userid"))
	if !ok {
		return
	}

	userID := target.ID
	filter := storage.ItemFilter{
		UserID:     &userID,
		Resume:     true,
		Recursive:  true,
		Limit:      unboundedLimit,
		SearchText: q.Get("searchterm"),
	}
	if err := s.scopedFilter(r.Context(), target, &filter); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if strings.EqualFold(q.Get("excludeactivesessions"), "true") {
		filter.ExcludeIDs = append(filter.ExcludeIDs, s.sessions.nowPlayingFor(string(userID))...)
	}

	include := splitCSV(q["includeitemtypes"])
	if len(include) > 0 {
		for _, t := range include {
			filter.ItemTypes = append(filter.ItemTypes, storage.ItemType(t))
		}
	} else {
		for _, t := range splitCSV(q["excludeitemtypes"]) {
			filter.ExcludeItemTypes = append(filter.ExcludeItemTypes, storage.ItemType(t))
		}
	}
	if mt := splitCSV(q["mediatypes"]); len(mt) > 0 {
		media := mediaTypesFilter(mt)
		if len(filter.ItemTypes) == 0 {
			filter.ItemTypes = media
		} else {
			var both []storage.ItemType
			for _, a := range filter.ItemTypes {
				for _, b := range media {
					if a == b {
						both = append(both, a)
					}
				}
			}
			if both == nil {
				both = []storage.ItemType{mediaTypesNoMatch}
			}
			filter.ItemTypes = both
		}
	}

	if v := q.Get("parentid"); v != "" {
		if _, err := s.store.GetLibrary(r.Context(), storage.LibraryID(v)); err == nil {
			libID := storage.LibraryID(v)
			filter.LibraryID = &libID
		} else {
			id := storage.ItemID(v)
			filter.ParentID = &id
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if v := q.Get("startindex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}

	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	withUserData := !strings.EqualFold(q.Get("enableuserdata"), "false")
	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		dto := s.baseItemDto(r.Context(), it)
		switch {
		case !withUserData:
			dto.UserData = nil
		case userID != ac.User.ID:
			dto.UserData = s.userDataFor(r.Context(), userID, it)
		}
		items = append(items, dto)
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: resolveTotalRecordCount(q.Get("enabletotalrecordcount"), len(items), page.TotalCount),
		StartIndex:       page.Offset,
	})
}
