// search.go implements GET /Search/Hints (docs/contract/specs/search.md): a
// ranked search over media items and over the genre, studio and people
// names of what the caller can see, answered as compact hints.
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// searchHintDto mirrors the reference's SearchHint: only the members that
// have a value are present (Artists and ChannelId always are).
type searchHintDto struct {
	ItemID                  string   `json:"ItemId"`
	ID                      string   `json:"Id"`
	Name                    string   `json:"Name"`
	MatchedTerm             string   `json:"MatchedTerm,omitempty"`
	IndexNumber             *int     `json:"IndexNumber,omitempty"`
	ProductionYear          *int     `json:"ProductionYear,omitempty"`
	ParentIndexNumber       *int     `json:"ParentIndexNumber,omitempty"`
	PrimaryImageTag         string   `json:"PrimaryImageTag,omitempty"`
	ThumbImageTag           string   `json:"ThumbImageTag,omitempty"`
	ThumbImageItemID        string   `json:"ThumbImageItemId,omitempty"`
	BackdropImageTag        string   `json:"BackdropImageTag,omitempty"`
	BackdropImageItemID     string   `json:"BackdropImageItemId,omitempty"`
	Type                    string   `json:"Type"`
	IsFolder                bool     `json:"IsFolder,omitempty"`
	RunTimeTicks            *int64   `json:"RunTimeTicks,omitempty"`
	MediaType               string   `json:"MediaType"`
	StartDate               string   `json:"StartDate,omitempty"`
	EndDate                 string   `json:"EndDate,omitempty"`
	Series                  string   `json:"Series,omitempty"`
	Status                  string   `json:"Status,omitempty"`
	Album                   string   `json:"Album,omitempty"`
	AlbumID                 string   `json:"AlbumId,omitempty"`
	AlbumArtist             string   `json:"AlbumArtist,omitempty"`
	Artists                 []string `json:"Artists"`
	SongCount               *int     `json:"SongCount,omitempty"`
	EpisodeCount            *int     `json:"EpisodeCount,omitempty"`
	ChannelID               *string  `json:"ChannelId"`
	ChannelName             string   `json:"ChannelName,omitempty"`
	PrimaryImageAspectRatio *float64 `json:"PrimaryImageAspectRatio,omitempty"`
}

type searchHintResult struct {
	SearchHints      []searchHintDto `json:"SearchHints"`
	TotalRecordCount int             `json:"TotalRecordCount"`
}

// hintFor builds the hint of a media item.
func (s *Server) hintFor(ctx context.Context, item storage.Item) searchHintDto {
	tags, ratio, _, _ := s.imageTagsFor(ctx, item.ID)
	h := searchHintDto{
		ItemID:            string(item.ID),
		ID:                string(item.ID),
		Name:              item.Name,
		IndexNumber:       item.IndexNumber,
		ProductionYear:    item.ProductionYear,
		ParentIndexNumber: item.ParentIndexNumber,
		PrimaryImageTag:   tags["Primary"],
		Type:              string(item.Type),
		IsFolder:          isFolderType(item.Type),
		RunTimeTicks:      item.RunTimeTicks,
		MediaType:         mediaTypeFor(item.Type),
		Artists:           []string{},
		Album:             item.Album,
		AlbumArtist:       item.AlbumArtist,
	}
	if h.PrimaryImageTag != "" {
		h.PrimaryImageAspectRatio = ratio
	}
	if item.Type == storage.ItemTypePlaylist {
		h.MediaType = s.playlistMediaType(ctx, item.ID)
	}
	if tags["Thumb"] != "" {
		h.ThumbImageTag, h.ThumbImageItemID = tags["Thumb"], string(item.ID)
	}
	if item.SeriesID != nil {
		if ser, err := s.store.GetItem(ctx, *item.SeriesID); err == nil {
			h.Series = ser.Name
			if h.ProductionYear == nil {
				h.ProductionYear = ser.ProductionYear
			}
		}
	}
	return h
}

// hintForNamed builds the hint of a genre, studio or person.
func hintForNamed(ref storage.NamedRef) searchHintDto {
	return searchHintDto{
		ItemID: string(ref.ID), ID: string(ref.ID), Name: ref.Name,
		Type: string(ref.Kind), MediaType: "Unknown", Artists: []string{},
	}
}

// parseBoolParam reads an optional boolean query parameter; ok is false
// (and a 400 has been written) when it is present and not a boolean.
func parseBoolParam(w http.ResponseWriter, vals map[string][]string, key, wire string) (value bool, given, ok bool) {
	raw := ""
	if v := vals[key]; len(v) > 0 {
		raw = strings.TrimSpace(v[0])
	}
	if raw == "" {
		return false, false, true
	}
	switch strings.ToLower(raw) {
	case "true":
		return true, true, true
	case "false":
		return false, true, true
	}
	writeValidationProblem(w, wire, "The value '"+raw+"' is not valid.")
	return false, false, false
}

// handleSearchHints is GET /Search/Hints.
func (s *Server) handleSearchHints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	if strings.TrimSpace(q.Get("searchterm")) == "" {
		writeValidationProblem(w, "searchTerm", "The searchTerm field is required.")
		return
	}
	term := strings.TrimSpace(q.Get("searchterm"))
	user, ok := s.itemsContextUser(w, r, q.Get("userid"))
	if !ok {
		return
	}
	limit, err := itemsInt(q, "limit")
	if err != nil {
		writeValidationProblem(w, "limit", "The value '"+q.Get("limit")+"' is not valid.")
		return
	}
	start, err := itemsInt(q, "startindex")
	if err != nil {
		writeValidationProblem(w, "startIndex", "The value '"+q.Get("startindex")+"' is not valid.")
		return
	}
	flag := func(key, wire string) (v, given, ok bool) { return parseBoolParam(w, q, key, wire) }
	includePeople, givenP, ok1 := flag("includepeople", "includePeople")
	includeMedia, givenM, ok2 := flag("includemedia", "includeMedia")
	includeGenres, givenG, ok3 := flag("includegenres", "includeGenres")
	includeStudios, givenS, ok4 := flag("includestudios", "includeStudios")
	if _, _, ok := flag("includeartists", "includeArtists"); !ok || !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	if !givenP {
		includePeople = true
	}
	if !givenM {
		includeMedia = true
	}
	if !givenG {
		includeGenres = true
	}
	if !givenS {
		includeStudios = true
	}
	// Flags only live TV programs carry: asking for them matches nothing.
	emptyFlags := false
	for _, kv := range [][2]string{{"ismovie", "isMovie"}, {"isseries", "isSeries"}, {"isnews", "isNews"}, {"iskids", "isKids"}, {"issports", "isSports"}} {
		v, _, ok := flag(kv[0], kv[1])
		if !ok {
			return
		}
		if v && kv[0] != "ismovie" {
			emptyFlags = true
		}
	}
	var parent storage.ItemID
	hasParent := false
	if raw := strings.TrimSpace(q.Get("parentid")); raw != "" {
		g, valid := normalizeGUID(raw)
		if !valid {
			writeInvalidID(w, "parentId", raw)
			return
		}
		if g != zeroGUID {
			parent, hasParent = storage.ItemID(g), true
		}
	}

	hints := searchHintResult{SearchHints: []searchHintDto{}}
	done := func() { writeJSON(w, http.StatusOK, hints) }
	if emptyFlags || (limit != nil && *limit <= 0) {
		done()
		return
	}

	sc, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if ac, ok := AuthFromContext(ctx); ok && ac.User.ID != user.ID {
		cp := *ac
		cp.User = user
		ctx = contextWithAuth(ctx, &cp)
	}

	// Which kinds of item take part.
	parseKinds := func(key string) []storage.ItemType {
		var out []storage.ItemType
		for _, tok := range itemsListParam(q, key) {
			if k, ok := itemsKnownKinds[strings.ToLower(tok)]; ok {
				out = append(out, k)
			}
		}
		return out
	}
	types := parseKinds("includeitemtypes")
	exclude := parseKinds("excludeitemtypes")
	contains := func(list []storage.ItemType, t storage.ItemType) bool {
		for _, x := range list {
			if x == t {
				return true
			}
		}
		return false
	}
	var kinds []storage.NamedKind
	step := func(enabled bool, t storage.ItemType, kind storage.NamedKind) {
		if enabled && (len(types) == 0 || contains(types, t)) {
			if !includeMedia {
				types = append(types, t)
			}
			kinds = append(kinds, kind)
			return
		}
		exclude = append(exclude, t)
	}
	step(includeGenres, storage.ItemTypeGenre, storage.NamedGenre)
	step(includeStudios, storage.ItemTypeStudio, storage.NamedStudio)
	step(includePeople, storage.ItemTypePerson, storage.NamedPerson)
	if hasParent {
		kinds = nil // names are by-name items: not under a parent
	}
	if len(types) > 0 {
		exclude = nil
	}

	f := storage.ItemFilter{}
	mediaTypesGiven := false
	var mediaKinds []storage.ItemType
	for _, tok := range itemsListParam(q, "mediatypes") {
		if ks, ok := mediaTypeKinds[strings.ToLower(tok)]; ok {
			mediaTypesGiven = true
			mediaKinds = append(mediaKinds, ks...)
		}
	}
	noMedia := false
	var mediaTypesWanted []storage.ItemType
	for _, t := range types {
		switch t {
		case storage.ItemTypeGenre, storage.ItemTypeStudio, storage.ItemTypePerson, storage.ItemTypeMusicGenre:
		default:
			mediaTypesWanted = append(mediaTypesWanted, t)
		}
	}
	if len(types) > 0 && len(mediaTypesWanted) == 0 {
		noMedia = true
	}
	// Only the kinds named by the type list take part by name.
	if len(types) > 0 {
		var keep []storage.NamedKind
		for _, k := range kinds {
			if contains(types, storage.ItemType(k)) {
				keep = append(keep, k)
			}
		}
		kinds = keep
	}
	f.ItemTypes = mediaTypesWanted
	f.ExcludeItemTypes = append(exclude, storage.ItemTypeYear, storage.ItemTypeFolder)
	if mediaTypesGiven {
		kinds = nil
		if len(f.ItemTypes) == 0 {
			f.ItemTypes = mediaKinds
		} else {
			var inter []storage.ItemType
			for _, a := range f.ItemTypes {
				if contains(mediaKinds, a) {
					inter = append(inter, a)
				}
			}
			f.ItemTypes = inter
		}
		if len(f.ItemTypes) == 0 {
			noMedia = true
		}
	}
	sc.apply(&f)
	if hasParent {
		if lib, err := s.store.GetLibrary(ctx, storage.LibraryID(parent)); err == nil {
			id := lib.ID
			f.LibraryID = &id
		} else if it, err := s.store.GetItem(ctx, parent); err == nil {
			pid := it.ID
			f.ParentID, f.Recursive = &pid, true
		} else {
			f.NoMatch = true
		}
	}

	// The page is cut the way the reference cuts it: Limit keeps the first
	// results, StartIndex then skips from those; the total counts what
	// Limit kept.
	sq := storage.SearchQuery{Items: f, Term: term, Kinds: kinds, NoMedia: noMedia, KeepPersonCase: true}
	skip := 0
	if start != nil && *start > 0 {
		skip = *start
	}
	if limit != nil {
		sq.Limit = *limit
	} else {
		sq.Offset = skip
		skip = 0
	}
	page, err := s.store.SearchHints(ctx, sq)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	hints.TotalRecordCount = page.Total
	if limit != nil && page.Total > *limit {
		hints.TotalRecordCount = *limit
	}
	if skip > len(page.Hits) {
		skip = len(page.Hits)
	}
	for _, h := range page.Hits[skip:] {
		switch {
		case h.Item != nil:
			hints.SearchHints = append(hints.SearchHints, s.hintFor(ctx, *h.Item))
		case h.Named != nil:
			hints.SearchHints = append(hints.SearchHints, hintForNamed(*h.Named))
		}
	}
	done()
}
