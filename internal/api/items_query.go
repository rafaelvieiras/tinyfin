// items_query.go implements the item listing and single-item read of the
// items contract (docs/contract/specs/items.md): GET /Items, its legacy
// per-user alias GET /Users/{userId}/Items, GET /Items/{itemId} and the
// legacy GET /Users/{userId}/Items/{itemId}.
//
// The listing resolves, in order: the context user (the token user, or
// UserId for an administrator), the scope (library views, a parent, or a
// recursive query over the libraries the user may access), the filters,
// sorting and paging, and finally the item shape (Fields, EnableUserData,
// image options). Library access (policy EnableAllFolders/EnabledFolders)
// and blocked/allowed tags are applied to every path that returns items,
// including selection by Ids.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/rating"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const zeroGUID = guid.Zero

// itemsKnownKinds is the set of item kind names accepted in
// IncludeItemTypes/ExcludeItemTypes; tokens outside it are dropped.
var itemsKnownKinds = func() map[string]storage.ItemType {
	m := map[string]storage.ItemType{}
	for _, k := range []string{
		"AggregateFolder", "Audio", "AudioBook", "BasePluginFolder", "Book", "BoxSet", "Channel",
		"ChannelFolderItem", "CollectionFolder", "Episode", "Folder", "Genre", "ManualPlaylistsFolder",
		"Movie", "LiveTvChannel", "LiveTvProgram", "MusicAlbum", "MusicArtist", "MusicGenre",
		"MusicVideo", "Person", "Photo", "PhotoAlbum", "Playlist", "PlaylistsFolder", "Program",
		"Recording", "Season", "Series", "Studio", "Trailer", "TvChannel", "TvProgram",
		"UserRootFolder", "UserView", "Video", "Year",
	} {
		m[strings.ToLower(k)] = storage.ItemType(k)
	}
	return m
}()

// mediaTypeKinds maps a MediaTypes value to the item kinds that carry it.
// Types with no kind behind them match nothing.
var mediaTypeKinds = map[string][]storage.ItemType{
	"video":   {storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeTrailer, storage.ItemTypeVideo},
	"audio":   {storage.ItemTypeAudio},
	"photo":   {storage.ItemTypePhoto},
	"book":    nil,
	"unknown": nil,
}

// itemsListFilterParams are the query parameters that turn a bare
// GET /Items into a query (items.list.R4): any of them present disables
// the library-views form. Recursive is handled separately.
var itemsListFilterParams = map[string]bool{
	"includeitemtypes": true, "excludeitemtypes": true, "mediatypes": true,
	"searchterm": true, "namestartswith": true, "namestartswithorgreater": true, "namelessthan": true,
	"ids": true, "excludeitemids": true, "adjacentto": true,
	"filters": true, "isplayed": true, "isfavorite": true,
	"years": true, "parentindexnumber": true, "indexnumber": true,
	"minwidth": true, "minheight": true, "maxwidth": true, "maxheight": true, "ishd": true, "is4k": true,
	"genres": true, "tags": true, "officialratings": true, "person": true, "hassubtitles": true,
	"hasoverview": true, "hasimdbid": true, "hastmdbid": true, "hasparentalrating": true,
	"mincommunityrating": true, "minpremieredate": true, "imagetypes": true, "islocked": true,
	"videotypes": true, "is3d": true, "maxofficialrating": true, "minofficialrating": true,
	"collapseboxsetitems": true, "studios": true, "artists": true, "albums": true,
	"personids": true, "persontypes": true, "genreids": true, "studioids": true,
}

// userRootItemID and playlistsFolderItemID are the stable ids of the two
// synthetic folders: the user's root and the Playlists folder shown beside
// the libraries.
var userRootItemID, playlistsFolderItemID = func() (string, string) {
	return guid.Derive("tinyfin-user-root"), guid.Derive("tinyfin-playlists-folder")
}()

// writeItemsProblem writes an RFC 9457 style problem document (the body
// the reference server sends with an unknown or hidden item).
func writeItemsProblem(w http.ResponseWriter, status int, title string) {
	typ := problemTypeNotFound
	if status == http.StatusBadRequest {
		typ = problemTypeBadRequest
	}
	writeProblemDoc(w, status, typ, title, nil)
}

// normalizeGUID returns s in the canonical wire form (32 lowercase hex
// digits), or false when s is not a GUID in either spelling.
func normalizeGUID(s string) (string, bool) { return guid.Normalize(s) }

// itemsListParam collects every value of key (repeated keys and
// comma-separated values alike), trimmed, empty tokens removed.
func itemsListParam(q url.Values, key string) []string {
	var out []string
	for _, v := range q[key] {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.TrimSpace(tok); tok != "" {
				out = append(out, tok)
			}
		}
	}
	return out
}

// itemsParamError is a request parameter that failed binding (400).
type itemsParamError struct{}

func (itemsParamError) Error() string { return "invalid query parameter" }

func itemsBool(q url.Values, key string) (*bool, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	switch strings.ToLower(v) {
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, itemsParamError{}
}

func itemsInt(q url.Values, key string) (*int, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, itemsParamError{}
	}
	return &n, nil
}

// itemsContextUser resolves the user whose libraries and user data shape
// the response (items.user.R1-R5). rawUserID is the legacy path segment or
// the UserId query value. ok is false when a response was already written.
func (s *Server) itemsContextUser(w http.ResponseWriter, r *http.Request, rawUserID string) (storage.User, bool) {
	ac, _ := AuthFromContext(r.Context())
	if strings.TrimSpace(rawUserID) == "" {
		return ac.User, true
	}
	norm, ok := normalizeGUID(rawUserID)
	if !ok {
		writeInvalidID(w, "userId", rawUserID)
		return storage.User{}, false
	}
	if norm == zeroGUID || norm == string(ac.User.ID) {
		return ac.User, true
	}
	if !ac.User.IsAdministrator {
		w.WriteHeader(http.StatusForbidden)
		return storage.User{}, false
	}
	u, err := s.store.GetUser(r.Context(), storage.UserID(norm))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return storage.User{}, false
	}
	return u, true
}

// itemsScope is what a context user may see: the libraries they have
// access to and the tag restrictions of their policy.
type itemsScope struct {
	libs        []storage.Library
	libIDs      map[storage.LibraryID]bool
	blockedTags []string
	allowedTags []string
	// maxRating is the policy's MaxParentalRating (+ sub rating); nil means
	// no limit. blockUnrated holds the item types whose unrated items the
	// policy's BlockUnratedItems hides.
	maxRating    *rating.Score
	blockUnrated []storage.ItemType
}

func (s *Server) itemsScopeFor(ctx context.Context, u storage.User) (itemsScope, error) {
	libs, err := s.store.QueryLibraries(ctx)
	if err != nil {
		return itemsScope{}, err
	}
	p := storedUserPolicy(u)
	sc := itemsScope{libIDs: map[storage.LibraryID]bool{}, blockedTags: p.BlockedTags, allowedTags: p.AllowedTags}
	if p.MaxParentalRating != nil {
		m := rating.Score{Value: *p.MaxParentalRating}
		if p.MaxParentalSubRating != nil {
			m.Sub = *p.MaxParentalSubRating
		}
		sc.maxRating = &m
	}
	sc.blockUnrated = unratedBlockTypes(p.BlockUnratedItems)
	enabled := map[string]bool{}
	for _, id := range p.EnabledFolders {
		enabled[strings.ToLower(id)] = true
	}
	blocked := map[string]bool{}
	for _, id := range p.BlockedMediaFolders {
		blocked[strings.ToLower(id)] = true
	}
	for _, lib := range libs {
		id := strings.ToLower(string(lib.ID))
		allowed := false
		if p.EnableAllFolders {
			allowed = !blocked[id]
		} else {
			allowed = enabled[id]
		}
		if allowed {
			sc.libs = append(sc.libs, lib)
			sc.libIDs[lib.ID] = true
		}
	}
	return sc, nil
}

func (sc itemsScope) libraryIDs() []storage.LibraryID {
	out := make([]storage.LibraryID, 0, len(sc.libs))
	for _, l := range sc.libs {
		out = append(out, l.ID)
	}
	return out
}

// apply narrows a query to what the scope lets the user see: accessible
// libraries, tag restrictions and the parental limits. Every item read path
// builds its filter through this, so the restrictions stay in one place.
func (sc itemsScope) apply(f *storage.ItemFilter) {
	f.RestrictToLibraries = true
	f.LibraryIDs = sc.libraryIDs()
	f.BlockedTags = sc.blockedTags
	f.AllowedTags = sc.allowedTags
	f.ParentalMax = sc.maxRating
	f.BlockUnratedTypes = sc.blockUnrated
}

// scopedFilter applies user's scope to f.
func (s *Server) scopedFilter(ctx context.Context, u storage.User, f *storage.ItemFilter) error {
	sc, err := s.itemsScopeFor(ctx, u)
	if err != nil {
		return err
	}
	sc.apply(f)
	return nil
}

// unratedBlockTypes expands the policy's BlockUnratedItems groups into item
// types. Movie covers movies; Series covers series, seasons and episodes;
// Trailer, Music and Other likewise (Other is every type not named by
// another group).
func unratedBlockTypes(groups []string) []storage.ItemType {
	var out []storage.ItemType
	seen := map[storage.ItemType]bool{}
	add := func(ts ...storage.ItemType) {
		for _, t := range ts {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	for _, g := range groups {
		switch strings.ToLower(g) {
		case "movie":
			add(storage.ItemTypeMovie)
		case "series":
			add(storage.ItemTypeSeries, storage.ItemTypeSeason, storage.ItemTypeEpisode)
		case "trailer":
			add(storage.ItemTypeTrailer)
		case "music":
			add(storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum, storage.ItemTypeAudio)
		case "other":
			add(storage.ItemTypeBoxSet, storage.ItemTypePlaylist, storage.ItemTypePerson, storage.ItemTypeGenre,
				storage.ItemTypeMusicGenre, storage.ItemTypeStudio, storage.ItemTypeYear, storage.ItemTypeFolder,
				storage.ItemTypeCollectionFolder, storage.ItemTypeUserView)
		}
	}
	return out
}

// callerScope is the scope of the authenticated caller (an API key has no
// user and so no restrictions).
func (s *Server) callerScope(ctx context.Context) (itemsScope, error) {
	var u storage.User
	if ac, ok := AuthFromContext(ctx); ok && ac != nil {
		u = ac.User
	}
	return s.itemsScopeFor(ctx, u)
}

// requireReadable answers 404 and reports false when the caller may not read
// item (library, tag and parental rules); the answer is the same as for a
// missing item.
func (s *Server) requireReadable(w http.ResponseWriter, r *http.Request, item storage.Item) bool {
	sc, err := s.callerScope(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	if !s.itemReadable(r.Context(), sc, item) {
		w.WriteHeader(http.StatusNotFound)
		return false
	}
	return true
}

// itemReadable is the single-item gate: the library and tag rules of
// itemVisible plus the parental rules. Unlike the listing filter it compares
// the rating value only (the sub rating is ignored), also requires the
// series' own rating to fit, and does not apply the unrated block to
// folder-like items (items.rating.R9).
func (s *Server) itemReadable(ctx context.Context, sc itemsScope, item storage.Item) bool {
	if !sc.itemVisible(item) {
		return false
	}
	own, ownOK := rating.Parse(item.OfficialRating)
	eff, effOK := own, ownOK
	var ser rating.Score
	serOK := false
	if item.SeriesID != nil {
		if parent, err := s.store.GetItem(ctx, *item.SeriesID); err == nil {
			ser, serOK = rating.Parse(parent.OfficialRating)
		}
		if !effOK {
			eff, effOK = ser, serOK
		}
	}
	if sc.maxRating != nil {
		if effOK && eff.ExceedsValue(*sc.maxRating) {
			return false
		}
		if serOK && ser.ExceedsValue(*sc.maxRating) {
			return false
		}
	}
	if !effOK {
		switch item.Type {
		case storage.ItemTypeMovie, storage.ItemTypeSeries, storage.ItemTypeSeason, storage.ItemTypeEpisode,
			storage.ItemTypeTrailer, storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum, storage.ItemTypeAudio:
			for _, t := range sc.blockUnrated {
				if t == item.Type {
					return false
				}
			}
		}
	}
	return true
}

// itemVisible reports whether item is inside an accessible library and
// passes the user's tag restrictions.
func (sc itemsScope) itemVisible(item storage.Item) bool {
	if !sc.libIDs[item.LibraryID] {
		return false
	}
	has := func(set []string) bool {
		for _, t := range item.Tags {
			for _, b := range set {
				if strings.EqualFold(t, b) {
					return true
				}
			}
		}
		return false
	}
	if has(sc.blockedTags) {
		return false
	}
	if len(sc.allowedTags) > 0 && !has(sc.allowedTags) {
		return false
	}
	return true
}

// itemsQuery is the parsed GET /Items request.
type itemsQuery struct {
	filter      storage.ItemFilter
	hasParent   bool
	recursive   *bool
	filterKeys  bool
	limit       int
	limitGiven  bool
	startIndex  int
	fields      map[string]bool
	userData    bool
	images      bool
	imageTypes  map[string]bool
	imageLimit  *int
	totalRecord string
}

// parseItemsQuery binds the listing parameters. legacy marks the
// /Users/{userId}/Items alias, which does not take IndexNumber.
func parseItemsQuery(q url.Values, legacy bool) (itemsQuery, error) {
	var iq itemsQuery
	f := &iq.filter

	for k := range q {
		if itemsListFilterParams[k] {
			iq.filterKeys = true
		}
	}

	rec, err := itemsBool(q, "recursive")
	if err != nil {
		return iq, err
	}
	iq.recursive = rec

	// Paging.
	limit, err := itemsInt(q, "limit")
	if err != nil {
		return iq, err
	}
	iq.limit = unboundedLimit
	if limit != nil && *limit >= 0 {
		iq.limit, iq.limitGiven = *limit, true
	}
	start, err := itemsInt(q, "startindex")
	if err != nil {
		return iq, err
	}
	if start != nil && *start > 0 {
		iq.startIndex = *start
	}
	iq.totalRecord = q.Get("enabletotalrecordcount")

	// Kinds.
	parseKinds := func(key string) []storage.ItemType {
		var out []storage.ItemType
		for _, tok := range itemsListParam(q, key) {
			if k, ok := itemsKnownKinds[strings.ToLower(tok)]; ok {
				out = append(out, k)
			}
		}
		return out
	}
	include := parseKinds("includeitemtypes")
	exclude := parseKinds("excludeitemtypes")
	if len(include) > 0 {
		f.ItemTypes = include
	} else if len(exclude) > 0 {
		f.ExcludeItemTypes = exclude
	}
	iq.fields = map[string]bool{}
	for _, tok := range itemsListParam(q, "fields") {
		iq.fields[strings.ToLower(tok)] = true
	}

	// MediaTypes narrow the kind list.
	var mediaKinds []storage.ItemType
	mediaGiven := false
	for _, tok := range itemsListParam(q, "mediatypes") {
		kinds, ok := mediaTypeKinds[strings.ToLower(tok)]
		if !ok {
			continue
		}
		mediaGiven = true
		mediaKinds = append(mediaKinds, kinds...)
	}
	if mediaGiven {
		if len(f.ItemTypes) == 0 {
			f.ItemTypes = mediaKinds
		} else {
			var inter []storage.ItemType
			for _, a := range f.ItemTypes {
				for _, b := range mediaKinds {
					if a == b {
						inter = append(inter, a)
					}
				}
			}
			f.ItemTypes = inter
		}
		if len(f.ItemTypes) == 0 {
			f.NoMatch = true
		}
	}

	// Ids, exclusions and the neighbour anchor.
	for _, tok := range itemsListParam(q, "ids") {
		if g, ok := normalizeGUID(tok); ok {
			f.IDs = append(f.IDs, storage.ItemID(g))
		}
	}
	f.OrderByIDs = len(f.IDs) > 1
	for _, tok := range itemsListParam(q, "excludeitemids") {
		if g, ok := normalizeGUID(tok); ok {
			f.ExcludeIDs = append(f.ExcludeIDs, storage.ItemID(g))
		}
	}
	if v := strings.TrimSpace(q.Get("adjacentto")); v != "" {
		if g, ok := normalizeGUID(v); ok {
			id := storage.ItemID(g)
			f.AdjacentTo = &id
		} else {
			f.NoMatch = true
		}
	}

	// Names and search.
	f.NameStartsWith = q.Get("namestartswith")
	f.NameStartsWithOrGreater = q.Get("namestartswithorgreater")
	f.NameLessThan = q.Get("namelessthan")
	f.NameContains = strings.TrimSpace(q.Get("searchterm"))

	// Numbers.
	for _, tok := range itemsListParam(q, "years") {
		if n, err := strconv.Atoi(tok); err == nil {
			f.Years = append(f.Years, n)
		}
	}
	if f.ParentIndexNumber, err = itemsInt(q, "parentindexnumber"); err != nil {
		return iq, err
	}
	if !legacy {
		if f.IndexNumber, err = itemsInt(q, "indexnumber"); err != nil {
			return iq, err
		}
	}
	for key, dst := range map[string]**int{
		"minwidth": &f.MinWidth, "minheight": &f.MinHeight, "maxwidth": &f.MaxWidth, "maxheight": &f.MaxHeight,
	} {
		if *dst, err = itemsInt(q, key); err != nil {
			return iq, err
		}
	}
	if f.IsHD, err = itemsBool(q, "ishd"); err != nil {
		return iq, err
	}
	if f.Is4K, err = itemsBool(q, "is4k"); err != nil {
		return iq, err
	}
	// 4K=false without HD=true selects a class that has no members.
	if f.Is4K != nil && !*f.Is4K && f.IsHD == nil {
		f.NoMatch = true
	}

	// Metadata.
	f.Genres = itemsListParam(q, "genres")
	f.Tags = itemsListParam(q, "tags")
	f.OfficialRatings = itemsListParam(q, "officialratings")
	// Rating bounds: unrecognized or "Unrated" values filter nothing.
	if sc, ok := rating.Parse(q.Get("maxofficialrating")); ok {
		f.RatingMax = &sc
	}
	if sc, ok := rating.Parse(q.Get("minofficialrating")); ok {
		f.RatingMin = &sc
	}
	f.PersonName = strings.TrimSpace(q.Get("person"))
	for key, dst := range map[string]**bool{
		"hasoverview": &f.HasOverview, "hassubtitles": &f.HasSubtitles, "hasimdbid": &f.HasImdbID,
		"hastmdbid": &f.HasTmdbID, "hasparentalrating": &f.HasParentalRating,
	} {
		if *dst, err = itemsBool(q, key); err != nil {
			return iq, err
		}
	}
	if v := strings.TrimSpace(q.Get("mincommunityrating")); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return iq, itemsParamError{}
		}
		f.MinCommunityRating = &n
	}
	if v := strings.TrimSpace(q.Get("minpremieredate")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return iq, itemsParamError{}
		}
		f.MinPremiereDate = &t
	}
	f.ImageTypes = itemsListParam(q, "imagetypes")
	// Locked/3D/video-type metadata is not tracked: asking for it matches
	// nothing; asking for its absence changes nothing.
	for _, key := range []string{"islocked", "is3d"} {
		b, err := itemsBool(q, key)
		if err != nil {
			return iq, err
		}
		if b != nil && *b {
			f.NoMatch = true
		}
	}
	if vt := itemsListParam(q, "videotypes"); len(vt) > 0 {
		match := false
		for _, v := range vt {
			if strings.EqualFold(v, "VideoFile") {
				match = true
			}
		}
		if !match {
			f.NoMatch = true
		}
	}

	// Per-user state.
	played, err := itemsBool(q, "isplayed")
	if err != nil {
		return iq, err
	}
	fav, err := itemsBool(q, "isfavorite")
	if err != nil {
		return iq, err
	}
	// The IsFolder query parameter is ignored (items.filter.R12); only the
	// Filters tokens select folders.
	var isFolder *bool
	flags := map[string]bool{}
	for _, tok := range itemsListParam(q, "filters") {
		flags[strings.ToLower(tok)] = true
	}
	for _, pair := range [][2]string{{"isfolder", "isnotfolder"}, {"isplayed", "isunplayed"}, {"likes", "dislikes"}} {
		if flags[pair[0]] && flags[pair[1]] {
			return iq, itemsParamError{}
		}
	}
	t, fl := true, false
	switch {
	case flags["isplayed"]:
		played = &t
	case flags["isunplayed"]:
		played = &fl
	}
	if flags["isfavorite"] || flags["isfavoriteorlikes"] {
		fav = &t
	}
	switch {
	case flags["isfolder"]:
		isFolder = &t
	case flags["isnotfolder"]:
		isFolder = &fl
	}
	if flags["likes"] || flags["dislikes"] {
		// Like/dislike ratings are not stored, so nothing can match.
		f.NoMatch = true
	}
	f.Played, f.Favorite, f.IsFolder = played, fav, isFolder
	f.Resumable = flags["isresumable"]

	// Sorting (items.sort.R1-R7).
	var keys []storage.SortField
	rawBy := itemsListParam(q, "sortby")
	rawOrder := itemsListParam(q, "sortorder")
	usable := 0
	for i, name := range rawBy {
		by, ok := itemsSortKeys[strings.ToLower(name)]
		if !ok {
			continue
		}
		usable++
		var order string
		switch {
		case i < len(rawOrder):
			order = rawOrder[i]
		case len(rawOrder) > 0:
			order = rawOrder[0]
		}
		keys = append(keys, storage.SortField{By: by, Descending: strings.EqualFold(order, "Descending")})
	}
	if usable > 0 && len(rawOrder) > usable {
		return iq, itemsParamError{}
	}
	f.SortBy = keys

	// Shaping.
	iq.userData = true
	if b, err := itemsBool(q, "enableuserdata"); err != nil {
		return iq, err
	} else if b != nil {
		iq.userData = *b
	}
	iq.images = true
	if b, err := itemsBool(q, "enableimages"); err != nil {
		return iq, err
	} else if b != nil {
		iq.images = *b
	}
	if iq.imageLimit, err = itemsInt(q, "imagetypelimit"); err != nil {
		return iq, err
	}
	if types := itemsListParam(q, "enableimagetypes"); len(types) > 0 {
		iq.imageTypes = map[string]bool{}
		for _, ty := range types {
			iq.imageTypes[strings.ToLower(ty)] = true
		}
	}
	return iq, nil
}

// itemsSortKeys is sortByValues (library.go) plus the aliases GET /Items
// also accepts, keyed by lower-cased wire name.
var itemsSortKeys = func() map[string]storage.SortBy {
	m := map[string]storage.SortBy{
		"name":    storage.SortByName,
		"runtime": storage.SortByRuntime,
	}
	for k, v := range sortByValues {
		m[strings.ToLower(k)] = v
	}
	return m
}()

// handleItemsList serves GET /Items and GET /Users/{userId}/Items.
func (s *Server) handleItemsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	pathUser := r.PathValue("userId")
	legacy := pathUser != ""
	rawUser := pathUser
	if !legacy {
		rawUser = q.Get("userid")
	}

	user, ok := s.itemsContextUser(w, r, rawUser)
	if !ok {
		return
	}
	iq, err := parseItemsQuery(q, legacy)
	if err != nil {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	scope, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	filter := iq.filter
	scope.apply(&filter)
	if filter.Played != nil || filter.Favorite != nil || filter.Resumable {
		uid := user.ID
		filter.UserID = &uid
	}

	views := false         // answer with the library views
	withPlaylists := false // ... plus the Playlists folder
	empty := false         // answer with nothing
	recursiveQuery := false

	if raw := strings.TrimSpace(q.Get("parentid")); raw != "" {
		norm, valid := normalizeGUID(raw)
		if !valid || norm == zeroGUID {
			writeInvalidID(w, "parentId", raw)
			return
		}
		switch {
		case norm == userRootItemID:
			views = true
		case norm == playlistsFolderItemID:
			empty = true
		default:
			if lib, err := s.store.GetLibrary(ctx, storage.LibraryID(norm)); err == nil {
				if !scope.libIDs[lib.ID] {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				id := lib.ID
				filter.LibraryID = &id
				recursiveQuery = len(filter.ItemTypes) > 0
				if iq.recursive != nil {
					recursiveQuery = *iq.recursive
				}
				if !recursiveQuery {
					filter.TopLevelOnly = true
					filter.RootFolderPaths = lib.Paths
				}
				iq.hasParent = true
				break
			}
			parent, err := s.store.GetItem(ctx, storage.ItemID(norm))
			if err != nil {
				writeBadRequest(w)
				return
			}
			if isFolderType(parent.Type) && !scope.libIDs[parent.LibraryID] {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !isFolderType(parent.Type) {
				// A leaf parent is ignored: handled as if no ParentId was
				// sent, without the Playlists extra.
				if !iq.filterKeys && (iq.recursive == nil || !*iq.recursive) {
					views = true
				} else {
					recursiveQuery = true
				}
				break
			}
			pid := parent.ID
			filter.ParentID = &pid
			if parent.Type == storage.ItemTypeSeason && parent.IndexNumber != nil && parent.SeriesID != nil {
				filter.SeasonMembers = &storage.SeasonMembers{SeriesID: *parent.SeriesID, Index: *parent.IndexNumber}
			}
			iq.hasParent = true
			recursiveQuery = iq.recursive != nil && *iq.recursive
			if !recursiveQuery {
				// The non-recursive child listing of a folder is not
				// narrowed by IncludeItemTypes (items.list.R8).
				filter.ItemTypes = nil
				filter.ExcludeItemTypes = nil
			}
		}
	} else if !iq.filterKeys && (iq.recursive == nil || !*iq.recursive) {
		views, withPlaylists = true, true
	} else {
		recursiveQuery = true
	}
	filter.Recursive = recursiveQuery

	startIndex := iq.startIndex
	shape := func(it baseItemDto) baseItemDto { return shapeListItem(it, iq) }

	if views {
		out := make([]baseItemDto, 0, len(scope.libs)+1)
		for _, lib := range scope.libs {
			out = append(out, shape(s.libraryBaseItemDto(ctx, lib)))
		}
		if withPlaylists {
			out = append(out, shape(s.playlistsFolderDto()))
		}
		writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: out, TotalRecordCount: len(out), StartIndex: startIndex})
		return
	}
	if empty {
		writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: []baseItemDto{}, StartIndex: startIndex})
		return
	}

	// A search ranks by relevance and ignores the requested sort.
	if filter.NameContains != "" {
		filter.SortBy = nil
	}

	filter.Offset = startIndex
	filter.Limit = iq.limit
	countOnly := iq.limitGiven && iq.limit == 0
	if countOnly {
		filter.Limit = 1
	}
	page, err := s.store.QueryItems(ctx, filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]baseItemDto, 0, len(page.Items))
	if !countOnly {
		for _, it := range page.Items {
			items = append(items, shape(s.baseItemDto(ctx, it)))
		}
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: resolveTotalRecordCount(iq.totalRecord, len(items), page.TotalCount),
		StartIndex:       startIndex,
	})
}

// playlistsFolderDto is the user's Playlists folder, listed after the
// libraries in the library-views form.
func (s *Server) playlistsFolderDto() baseItemDto {
	return baseItemDto{
		Name:           "Playlists",
		ServerID:       s.cfg.ServerID,
		ID:             playlistsFolderItemID,
		IsFolder:       true,
		Type:           "ManualPlaylistsFolder",
		CollectionType: "playlists",
	}
}

// userRootDto is the user's root folder (the all-zero item id).
func (s *Server) userRootDto() baseItemDto {
	return baseItemDto{
		Name:     "Media Folders",
		ServerID: s.cfg.ServerID,
		ID:       userRootItemID,
		IsFolder: true,
		Type:     "UserRootFolder",
	}
}

// shapeListItem applies the list-shaping options (Fields, EnableUserData,
// image options) to a listing item (items.fields.R2-R4). Optional fields
// are present only when requested through Fields.
func shapeListItem(d baseItemDto, iq itemsQuery) baseItemDto {
	has := func(name string) bool { return iq.fields[name] }
	if !has("path") {
		d.Path = ""
	}
	if !has("sortname") {
		d.SortName = ""
	}
	if !has("childcount") {
		d.ChildCount = nil
	}
	if !has("recursiveitemcount") {
		d.RecursiveItemCount = nil
	}
	if !has("overview") {
		d.Overview = ""
	}
	if !has("taglines") {
		d.Taglines = nil
	}
	if !has("genres") {
		d.Genres, d.GenreItems = nil, nil
	}
	if !has("people") {
		d.People = nil
	}
	if !has("studios") {
		d.Studios = nil
	}
	if !has("mediasources") {
		d.MediaSources = nil
	}
	if !has("mediastreams") {
		d.MediaStreams = nil
	}
	if !has("providerids") {
		d.ProviderIDs = nil
	}
	if !has("externalurls") {
		d.ExternalUrls = nil
	}
	if !has("tags") {
		d.Tags = nil
	}
	if !has("originaltitle") {
		d.OriginalTitle = ""
	}
	if !iq.userData {
		d.UserData = nil
	}
	if !iq.images || (iq.imageLimit != nil && *iq.imageLimit == 0) {
		d.ImageTags, d.BackdropImageTags, d.PrimaryImageAspectRatio = nil, nil, nil
	} else if iq.imageTypes != nil {
		for k := range d.ImageTags {
			if !iq.imageTypes[strings.ToLower(k)] {
				delete(d.ImageTags, k)
			}
		}
		if !iq.imageTypes["backdrop"] {
			d.BackdropImageTags = nil
		}
		if !iq.imageTypes["primary"] {
			d.PrimaryImageAspectRatio = nil
		}
	}
	return d
}

// handleItemGet serves GET /Items/{itemId} and the legacy
// GET /Users/{userId}/Items/{itemId}: the full item object for the context
// user (items.get.R1-R9). An item the user cannot see is a 404 problem
// document, indistinguishable from a missing one.
//
// A library root is not an Item row (storage.Library and storage.Item are
// separate stores), so a plain GetItem miss falls back to GetLibrary before
// 404ing — a jellyfin-android item-detail flow calls this route with a
// library's own id.
func (s *Server) handleItemGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawUser := r.PathValue("userId")
	if rawUser == "" {
		rawUser = r.URL.Query().Get("UserId")
		if rawUser == "" {
			rawUser = caseInsensitiveQuery(r).Get("userid")
		}
	}
	user, ok := s.itemsContextUser(w, r, rawUser)
	if !ok {
		return
	}
	scope, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	rawID := r.PathValue("itemId")
	notFound := func() { writeItemsProblem(w, http.StatusNotFound, "Not Found") }
	norm, valid := normalizeGUID(rawID)
	if !valid {
		// wire.problem.R2: an id that is not a GUID cannot be bound.
		writeInvalidID(w, "itemId", rawID)
		return
	}
	// The DTO builders read the caller from the request context, so a
	// request acting for another user is rewritten to carry that user.
	if ac, ok := AuthFromContext(ctx); ok && ac.User.ID != user.ID {
		cp := *ac
		cp.User = user
		ctx = contextWithAuth(ctx, &cp)
	}

	switch norm {
	case zeroGUID, userRootItemID:
		writeJSON(w, http.StatusOK, s.userRootDto())
		return
	case playlistsFolderItemID:
		writeJSON(w, http.StatusOK, s.playlistsFolderDto())
		return
	}
	item, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil {
		if lib, libErr := s.store.GetLibrary(ctx, storage.LibraryID(norm)); libErr == nil && scope.libIDs[lib.ID] {
			writeJSON(w, http.StatusOK, s.libraryBaseItemDto(ctx, lib))
			return
		}
		notFound()
		return
	}
	if !s.itemReadable(ctx, scope, item) {
		notFound()
		return
	}
	writeJSON(w, http.StatusOK, s.baseItemDto(ctx, item))
}
