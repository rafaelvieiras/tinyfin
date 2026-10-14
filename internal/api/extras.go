// extras.go serves the routes that list the extras of an item: its local
// trailers, its special features, its theme songs and theme videos and the
// additional parts of a stacked movie. The extras themselves are cataloged by
// the scanner (internal/scan/extras.go) as items that carry an ExtraType and
// an OwnerID; no listing, count or search returns them, only these routes (or
// their id) do.
package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// extrasOf returns the extras of owner whose ExtraType is in types (every
// extra when types is empty), ordered by name.
func (s *Server) extrasOf(ctx context.Context, owner storage.ItemID, types ...string) []storage.Item {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{OwnerID: &owner, ExtraTypes: types, Limit: 1000})
	if err != nil {
		return nil
	}
	items := page.Items
	// The reference orders extras by sort name; a video's is its lower-cased
	// name, an audio file's (a theme song) the name as it is.
	key := func(it storage.Item) string {
		if it.Type == storage.ItemTypeAudio {
			return it.Name
		}
		return strings.ToLower(it.Name)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := key(items[i]), key(items[j])
		if a != b {
			return a < b
		}
		return items[i].Path < items[j].Path
	})
	return items
}

// internalExtraType reports whether an ExtraType is one the wire does not
// show: an additional part, a version or a merged season folder.
func internalExtraType(t string) bool {
	return t == storage.ExtraTypePart || t == storage.ExtraTypeVersion || t == storage.ExtraTypeMergedSeason
}

// specialFeatureTypes are the ExtraType values counted as special features.
var specialFeatureTypes = []string{
	storage.ExtraTypeFeaturette, storage.ExtraTypeBehindTheScenes, storage.ExtraTypeDeletedScene,
	storage.ExtraTypeInterview, storage.ExtraTypeScene, storage.ExtraTypeShort, storage.ExtraTypeSample,
	storage.ExtraTypeClip, storage.ExtraTypeUnknown,
}

// versionSources are the media sources of the other files (versions) of a
// movie or episode, after its own: each one is a direct play source named
// after what sets the file apart ("4K"), carrying the id of its version item.
func (s *Server) versionSources(ctx context.Context, item storage.Item) []mediaSourceInfoDto {
	var out []mediaSourceInfoDto
	for _, v := range s.extrasOf(ctx, item.ID, storage.ExtraTypeVersion) {
		src := baseMediaSourceInfo(v, v.Container)
		s.applyStreamDefaults(ctx, &src, v.MediaStreams)
		src.Name = v.Name
		src.SupportsDirectPlay = true
		src.TranscodingSubProtocol = "http"
		out = append(out, src)
	}
	return out
}

// versionFor returns the version item a request names with MediaSourceId
// when it is one of item's versions.
func (s *Server) versionFor(ctx context.Context, r *http.Request, item storage.Item) (storage.Item, bool) {
	raw := caseInsensitiveQuery(r).Get("mediasourceid")
	if raw == "" {
		return item, false
	}
	norm, ok := normalizeGUID(raw)
	if !ok || norm == string(item.ID) {
		return item, false
	}
	v, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil || v.OwnerID == nil || *v.OwnerID != item.ID || v.ExtraType != storage.ExtraTypeVersion {
		return item, false
	}
	return v, true
}

// applyExtraInfo fills the fields of dto that depend on the extras of item:
// LocalTrailerCount and SpecialFeatureCount, and PartCount for a stacked
// movie. Done on demand (a single read, or a listing that names the fields)
// because each call is a query.
func (s *Server) applyExtraInfo(ctx context.Context, dto *baseItemDto, item storage.Item) {
	if item.ExtraType != "" && !internalExtraType(item.ExtraType) {
		zero := 0
		dto.LocalTrailerCount, dto.SpecialFeatureCount = &zero, &zero
		return
	}
	trailers, features, parts := 0, 0, 0
	for _, x := range s.extrasOf(ctx, item.ID) {
		switch x.ExtraType {
		case storage.ExtraTypeTrailer:
			trailers++
		case storage.ExtraTypePart:
			parts++
		case storage.ExtraTypeThemeSong, storage.ExtraTypeThemeVideo, storage.ExtraTypeVersion, storage.ExtraTypeMergedSeason:
		default:
			features++
		}
	}
	dto.LocalTrailerCount, dto.SpecialFeatureCount = &trailers, &features
	if len(dto.MediaSources) > 0 {
		dto.MediaSources = append(dto.MediaSources, s.versionSources(ctx, item)...)
	}
	if parts > 0 {
		n := parts + 1
		dto.PartCount = &n
	}
}

// extrasItem resolves the item of an extras route: the id (400 when it is not
// a GUID), the optional user (UserId query or legacy path segment) and the
// caller's right to see the item (404 otherwise). ok is false when a response
// was already written; lib is true when the id is a library root, which has
// no extras and answers an empty list.
func (s *Server) extrasItem(w http.ResponseWriter, r *http.Request) (ctx context.Context, item storage.Item, lib, ok bool) {
	ctx = r.Context()
	rawUser := r.PathValue("userId")
	if rawUser == "" {
		rawUser = caseInsensitiveQuery(r).Get("userid")
	}
	user, okUser := s.itemsContextUser(w, r, rawUser)
	if !okUser {
		return ctx, item, false, false
	}
	scope, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return ctx, item, false, false
	}
	rawID := r.PathValue("itemId")
	norm, valid := normalizeGUID(rawID)
	if !valid {
		writeInvalidID(w, "itemId", rawID)
		return ctx, item, false, false
	}
	if ac, found := AuthFromContext(ctx); found && ac.User.ID != user.ID {
		cp := *ac
		cp.User = user
		ctx = contextWithAuth(ctx, &cp)
	}
	item, err = s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil {
		if l, libErr := s.store.GetLibrary(ctx, storage.LibraryID(norm)); libErr == nil && scope.libIDs[l.ID] {
			return ctx, item, true, true
		}
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return ctx, item, false, false
	}
	if !s.itemReadable(ctx, scope, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return ctx, item, false, false
	}
	return ctx, item, false, true
}

func (s *Server) extraDtos(ctx context.Context, extras []storage.Item) []baseItemDto {
	out := make([]baseItemDto, 0, len(extras))
	for _, x := range extras {
		out = append(out, s.baseItemDto(ctx, x))
	}
	return out
}

// handleLocalTrailers serves GET /Items/{itemId}/LocalTrailers and the legacy
// GET /Users/{userId}/Items/{itemId}/LocalTrailers: a bare array.
func (s *Server) handleLocalTrailers(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	if lib {
		writeJSON(w, http.StatusOK, []baseItemDto{})
		return
	}
	writeJSON(w, http.StatusOK, s.extraDtos(ctx, s.extrasOf(ctx, item.ID, storage.ExtraTypeTrailer)))
}

// handleSpecialFeatures serves GET /Items/{itemId}/SpecialFeatures and its
// legacy user-scoped form: a bare array.
func (s *Server) handleSpecialFeatures(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	if lib {
		writeJSON(w, http.StatusOK, []baseItemDto{})
		return
	}
	writeJSON(w, http.StatusOK, s.extraDtos(ctx, s.extrasOf(ctx, item.ID, specialFeatureTypes...)))
}

// themeResult is the {OwnerId, Items, TotalRecordCount, StartIndex} envelope
// of the theme routes.
type themeResult struct {
	OwnerID          string        `json:"OwnerId"`
	Items            []baseItemDto `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
	StartIndex       int           `json:"StartIndex"`
}

// themesOf returns the theme extras of the given ExtraType for item. With
// inherit set and none of its own, the closest ancestor that has some
// supplies them (owner then names that ancestor).
func (s *Server) themesOf(ctx context.Context, item storage.Item, extraType string, inherit bool) (owner storage.ItemID, items []storage.Item) {
	owner = item.ID
	items = s.extrasOf(ctx, item.ID, extraType)
	cur := item
	for len(items) == 0 && inherit && cur.ParentID != nil {
		parent, err := s.store.GetItem(ctx, *cur.ParentID)
		if err != nil {
			break
		}
		if got := s.extrasOf(ctx, parent.ID, extraType); len(got) > 0 {
			return parent.ID, got
		}
		cur = parent
	}
	return owner, items
}

func (s *Server) themeResultFor(ctx context.Context, item storage.Item, extraType string, inherit bool) themeResult {
	owner, items := s.themesOf(ctx, item, extraType, inherit)
	dtos := s.extraDtos(ctx, items)
	return themeResult{OwnerID: string(owner), Items: dtos, TotalRecordCount: len(dtos)}
}

func inheritFromParent(r *http.Request) bool {
	return strings.EqualFold(caseInsensitiveQuery(r).Get("inheritfromparent"), "true")
}

// handleThemeSongs serves GET /Items/{itemId}/ThemeSongs.
func (s *Server) handleThemeSongs(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	if lib {
		writeJSON(w, http.StatusOK, themeResult{OwnerID: r.PathValue("itemId"), Items: []baseItemDto{}})
		return
	}
	writeJSON(w, http.StatusOK, s.themeResultFor(ctx, item, storage.ExtraTypeThemeSong, inheritFromParent(r)))
}

// handleThemeVideos serves GET /Items/{itemId}/ThemeVideos.
func (s *Server) handleThemeVideos(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	if lib {
		writeJSON(w, http.StatusOK, themeResult{OwnerID: r.PathValue("itemId"), Items: []baseItemDto{}})
		return
	}
	writeJSON(w, http.StatusOK, s.themeResultFor(ctx, item, storage.ExtraTypeThemeVideo, inheritFromParent(r)))
}

// handleThemeMedia serves GET /Items/{itemId}/ThemeMedia: both theme
// results at once and an always-empty soundtrack result.
func (s *Server) handleThemeMedia(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	inherit := inheritFromParent(r)
	resp := themeMediaResponse{
		SoundtrackSongsResult: themeResult{OwnerID: zeroGUID, Items: []baseItemDto{}},
	}
	if lib {
		resp.ThemeVideosResult = themeResult{OwnerID: r.PathValue("itemId"), Items: []baseItemDto{}}
		resp.ThemeSongsResult = themeResult{OwnerID: r.PathValue("itemId"), Items: []baseItemDto{}}
	} else {
		resp.ThemeVideosResult = s.themeResultFor(ctx, item, storage.ExtraTypeThemeVideo, inherit)
		resp.ThemeSongsResult = s.themeResultFor(ctx, item, storage.ExtraTypeThemeSong, inherit)
	}
	writeJSON(w, http.StatusOK, resp)
}

// themeMediaResponse is GET /Items/{itemId}/ThemeMedia's envelope.
type themeMediaResponse struct {
	ThemeVideosResult     themeResult `json:"ThemeVideosResult"`
	ThemeSongsResult      themeResult `json:"ThemeSongsResult"`
	SoundtrackSongsResult themeResult `json:"SoundtrackSongsResult"`
}

// handleAdditionalParts serves GET /Videos/{itemId}/AdditionalParts: the
// parts of a stacked movie after the first, as {Items, TotalRecordCount,
// StartIndex} (no OwnerId).
func (s *Server) handleAdditionalParts(w http.ResponseWriter, r *http.Request) {
	ctx, item, lib, ok := s.extrasItem(w, r)
	if !ok {
		return
	}
	items := []baseItemDto{}
	if !lib {
		parts := s.extrasOf(ctx, item.ID, storage.ExtraTypePart)
		sort.SliceStable(parts, func(i, j int) bool { return parts[i].Path < parts[j].Path })
		items = s.extraDtos(ctx, parts)
	}
	writeJSON(w, http.StatusOK, struct {
		Items            []baseItemDto `json:"Items"`
		TotalRecordCount int           `json:"TotalRecordCount"`
		StartIndex       int           `json:"StartIndex"`
	}{items, len(items), 0})
}
