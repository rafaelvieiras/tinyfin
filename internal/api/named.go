// named.go implements the browse routes over named items (genres, studios,
// people, years) and the filter facets (docs/contract/specs/browse.md):
//
//	GET /Genres, /Genres/{genreName}
//	GET /Studios, /Studios/{name}
//	GET /Persons, /Persons/{name}
//	GET /Years, /Years/{year}
//	GET /Items/Filters, /Items/Filters2
//
// A named item is not stored as an item: it exists while an item the caller
// can see carries the name, and its identifier is derived from the kind and
// the name (storage.NamedID). Opening one by identifier works through
// GET /Items/{itemId} (handleItemGet falls back to FindNamed).
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/playlist"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// namedItemID is the wire identifier of a named item.
func namedItemID(kind storage.NamedKind, name string) string {
	return string(storage.NamedID(kind, name))
}

// namedReq is a parsed browse request: who asks, what they may see, the
// listing parameters and the carrier scope.
type namedReq struct {
	user  storage.User
	scope itemsScope
	iq    itemsQuery
	q     url.Values
	// carriers selects the items whose names are listed.
	carriers storage.ItemFilter
	// noMatch is a parent that cannot hold anything.
	noMatch bool
}

// newNamedReq binds the common parameters of the browse listings. ok is
// false when a response has been written.
func (s *Server) newNamedReq(w http.ResponseWriter, r *http.Request) (namedReq, bool) {
	var nr namedReq
	ctx := r.Context()
	nr.q = caseInsensitiveQuery(r)
	user, ok := s.itemsContextUser(w, r, nr.q.Get("userid"))
	if !ok {
		return nr, false
	}
	nr.user = user
	iq, err := parseItemsQuery(nr.q, false)
	if err != nil {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return nr, false
	}
	nr.iq = iq
	sc, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return nr, false
	}
	nr.scope = sc

	var f storage.ItemFilter
	f.ItemTypes = iq.filter.ItemTypes
	f.ExcludeItemTypes = iq.filter.ExcludeItemTypes
	f.NoMatch = iq.filter.NoMatch
	sc.apply(&f)
	if raw := strings.TrimSpace(nr.q.Get("parentid")); raw != "" {
		norm, valid := normalizeGUID(raw)
		if !valid {
			writeInvalidID(w, "parentId", raw)
			return nr, false
		}
		switch {
		case norm == zeroGUID || norm == userRootItemID:
			// the root: every library
		case norm == playlistsFolderItemID || norm == playlistsViewID:
			lib := playlist.LibraryID()
			f.LibraryID = &lib
		default:
			if lib, err := s.store.GetLibrary(ctx, storage.LibraryID(norm)); err == nil {
				if !sc.libIDs[lib.ID] {
					nr.noMatch = true
				}
				id := lib.ID
				f.LibraryID = &id
			} else if parent, err := s.store.GetItem(ctx, storage.ItemID(norm)); err == nil && s.itemReadable(ctx, sc, parent) {
				pid := parent.ID
				f.ParentID = &pid
				f.Recursive = true
			} else {
				nr.noMatch = true
			}
		}
	}
	nr.carriers = f
	return nr, true
}

// namedDto is the wire form of a named item (genre, studio, person, year):
// the members the reference answers with, in its order. The list form carries
// identity and image slots; the single-item form adds the shell of metadata
// members, which are empty for a named item. Empty collections are emitted
// (non-nil), absent ones are omitted.
type namedDto struct {
	Name                     string                       `json:"Name"`
	ServerID                 string                       `json:"ServerId"`
	ID                       string                       `json:"Id"`
	Etag                     string                       `json:"Etag,omitempty"`
	DateCreated              *wireTime                    `json:"DateCreated,omitempty"`
	CanDelete                *bool                        `json:"CanDelete,omitempty"`
	CanDownload              *bool                        `json:"CanDownload,omitempty"`
	SortName                 string                       `json:"SortName,omitempty"`
	ExternalURLs             []externalURLDto             `json:"ExternalUrls,omitzero"`
	Path                     string                       `json:"Path,omitempty"`
	EnableMediaSourceDisplay *bool                        `json:"EnableMediaSourceDisplay,omitempty"`
	ChannelID                *string                      `json:"ChannelId"`
	Taglines                 []string                     `json:"Taglines,omitzero"`
	Genres                   []string                     `json:"Genres,omitzero"`
	PlayAccess               string                       `json:"PlayAccess,omitempty"`
	RemoteTrailers           []mediaURLDto                `json:"RemoteTrailers,omitzero"`
	ProviderIDs              map[string]string            `json:"ProviderIds,omitzero"`
	ParentID                 json.RawMessage              `json:"ParentId,omitzero"`
	ProductionYear           *int                         `json:"ProductionYear,omitempty"`
	Type                     string                       `json:"Type"`
	People                   []baseItemPersonDto          `json:"People,omitzero"`
	Studios                  []nameIDPairDto              `json:"Studios,omitzero"`
	GenreItems               []nameIDPairDto              `json:"GenreItems,omitzero"`
	LocalTrailerCount        *int                         `json:"LocalTrailerCount,omitempty"`
	UserData                 *userItemDataDto             `json:"UserData,omitempty"`
	ChildCount               *int                         `json:"ChildCount,omitempty"`
	SpecialFeatureCount      *int                         `json:"SpecialFeatureCount,omitempty"`
	DisplayPreferencesID     string                       `json:"DisplayPreferencesId,omitempty"`
	Tags                     []string                     `json:"Tags,omitzero"`
	ImageTags                map[string]string            `json:"ImageTags"`
	BackdropImageTags        []string                     `json:"BackdropImageTags"`
	ImageBlurHashes          map[string]map[string]string `json:"ImageBlurHashes"`
	Chapters                 []chapterInfoDto             `json:"Chapters,omitzero"`
	LocationType             string                       `json:"LocationType"`
	MediaType                string                       `json:"MediaType"`
	LockedFields             []string                     `json:"LockedFields,omitzero"`
	TrailerCount             *int                         `json:"TrailerCount,omitempty"`
	MovieCount               *int                         `json:"MovieCount,omitempty"`
	SeriesCount              *int                         `json:"SeriesCount,omitempty"`
	ProgramCount             *int                         `json:"ProgramCount,omitempty"`
	EpisodeCount             *int                         `json:"EpisodeCount,omitempty"`
	SongCount                *int                         `json:"SongCount,omitempty"`
	AlbumCount               *int                         `json:"AlbumCount,omitempty"`
	ArtistCount              *int                         `json:"ArtistCount,omitempty"`
	MusicVideoCount          *int                         `json:"MusicVideoCount,omitempty"`
	LockData                 *bool                        `json:"LockData,omitempty"`
}

// namedItemsResult is a query result whose items may be of different DTO
// shapes (a search over items and named items).
type namedItemsResult struct {
	Items            []any `json:"Items"`
	TotalRecordCount int   `json:"TotalRecordCount"`
	StartIndex       int   `json:"StartIndex"`
}

// namedDtoOpts shapes a named item DTO.
type namedDtoOpts struct {
	full     bool // the single-item form
	userData bool // UserData is present
	counts   bool // ChildCount and the per-type counts are present
}

// namedItemDto builds the DTO of a named item from its entry.
func (s *Server) namedItemDto(e storage.NamedEntry, o namedDtoOpts) namedDto {
	id := string(e.ID)
	dto := namedDto{
		Name:              e.Name,
		ServerID:          s.cfg.ServerID,
		ID:                id,
		Type:              string(e.Kind),
		MediaType:         "Unknown",
		LocationType:      "FileSystem",
		ImageTags:         map[string]string{},
		BackdropImageTags: []string{},
		ImageBlurHashes:   map[string]map[string]string{},
	}
	created := e.Created
	if created.IsZero() {
		created = time.Now().UTC()
	}
	sortName, pth := "", ""
	switch e.Kind {
	case storage.NamedGenre:
		sortName, pth = strings.ToLower(e.Name), path.Join("/metadata/Genre", e.Name)
	case storage.NamedStudio:
		sortName, pth = strings.ToLower(e.Name), path.Join("/metadata/Studio", e.Name)
	case storage.NamedPerson:
		first := "#"
		if r := []rune(e.Name); len(r) > 0 {
			first = strings.ToUpper(string(r[0]))
		}
		sortName, pth = e.Name, path.Join("/metadata/People", first, e.Name)
	case storage.NamedYear:
		if n, err := strconv.Atoi(e.Name); err == nil {
			sortName = fmt.Sprintf("%010d", n)
		}
		pth = path.Join("/metadata/Year", e.Name)
	}
	dto.SortName, dto.Path = sortName, pth
	dto.DateCreated = &wireTime{created}
	if o.userData {
		dto.UserData = &userItemDataDto{Key: string(e.Kind) + "-" + e.Name, ItemID: id}
	}
	if o.full {
		f, t := false, true
		dto.CanDelete, dto.CanDownload, dto.LockData, dto.EnableMediaSourceDisplay = &f, &f, &f, &t
		dto.PlayAccess = "Full"
		sum := sha256.Sum256([]byte("etag-named-" + id))
		dto.Etag = hex.EncodeToString(sum[:16])
		dto.DisplayPreferencesID = displayPreferencesIDFor(storage.ItemType(e.Kind))
		zero := 0
		dto.LocalTrailerCount, dto.SpecialFeatureCount = &zero, &zero
		dto.ExternalURLs, dto.Taglines, dto.Genres, dto.RemoteTrailers = []externalURLDto{}, []string{}, []string{}, []mediaURLDto{}
		dto.ProviderIDs, dto.People, dto.Studios, dto.GenreItems = map[string]string{}, []baseItemPersonDto{}, []nameIDPairDto{}, []nameIDPairDto{}
		dto.Tags, dto.Chapters, dto.LockedFields = []string{}, []chapterInfoDto{}, []string{}
		dto.ParentID = json.RawMessage("null")
	}
	if o.counts {
		n := e.Total
		dto.ChildCount = &n
		zero := 0
		movie, series, episode := e.Movies, e.Series, e.Episodes
		songs, albums, artists := e.Songs, e.Albums, e.Artists
		dto.TrailerCount, dto.ProgramCount, dto.SongCount = &zero, &zero, &songs
		dto.AlbumCount, dto.ArtistCount, dto.MusicVideoCount = &albums, &artists, &zero
		dto.MovieCount, dto.SeriesCount, dto.EpisodeCount = &movie, &series, &episode
	}
	return dto
}

// shapeNamed applies the list options to a named DTO: the date, path and
// sort name only when Fields asks for them, the counts only with ItemCounts
// or ChildCount (genres and studios; also when item types are named), user
// data and images as the request enables them.
func shapeNamed(d namedDto, iq itemsQuery, kind storage.NamedKind, music bool) namedDto {
	has := func(n string) bool { return iq.fields[n] }
	counts := false
	switch kind {
	case storage.NamedGenre:
		counts = has("itemcounts") || has("childcount") || (len(iq.filter.ItemTypes) > 0 && !music)
	case storage.NamedStudio:
		counts = has("itemcounts") || has("childcount") || len(iq.filter.ItemTypes) > 0
	}
	if !has("datecreated") {
		d.DateCreated = nil
	}
	if !has("path") {
		d.Path = ""
	}
	if !has("sortname") {
		d.SortName = ""
	}
	if !counts {
		d.TrailerCount, d.ProgramCount, d.SongCount = nil, nil, nil
		d.AlbumCount, d.ArtistCount, d.MusicVideoCount = nil, nil, nil
		d.MovieCount, d.SeriesCount, d.EpisodeCount = nil, nil, nil
		d.ChildCount = nil
	}
	if !iq.userData {
		d.UserData = nil
	}
	if !iq.images || (iq.imageLimit != nil && *iq.imageLimit == 0) {
		d.ImageTags, d.BackdropImageTags, d.ImageBlurHashes = map[string]string{}, []string{}, map[string]map[string]string{}
	}
	return d
}

// listNamed serves a named-item listing: the distinct names of kind carried
// by the caller's items, with paging, name filters and sorting.
func (s *Server) listNamed(w http.ResponseWriter, r *http.Request, kind storage.NamedKind, withUserData bool) {
	s.listNamedIn(w, r, kind, withUserData, namedScope{})
}

// namedScope narrows a genre listing to the music items (music) or leaves
// them out (noMusic), unless the request names item types itself.
type namedScope struct{ music, noMusic bool }

var musicItemTypes = []storage.ItemType{storage.ItemTypeAudio, storage.ItemTypeMusicAlbum, storage.ItemTypeMusicArtist}

func (s *Server) listNamedIn(w http.ResponseWriter, r *http.Request, kind storage.NamedKind, withUserData bool, ns namedScope) {
	nr, ok := s.newNamedReq(w, r)
	if !ok {
		return
	}
	if ns.noMusic && nr.carriers.LibraryID != nil {
		// A music library as the parent lists its genres, as music genres.
		if lib, err := s.store.GetLibrary(r.Context(), *nr.carriers.LibraryID); err == nil && lib.CollectionType == storage.CollectionTypeMusic {
			ns = namedScope{music: true}
		}
	}
	if ns.music && len(nr.carriers.ItemTypes) == 0 {
		nr.carriers.ItemTypes = musicItemTypes
	}
	if ns.noMusic && len(nr.carriers.ItemTypes) == 0 {
		nr.carriers.ExcludeItemTypes = append(append([]storage.ItemType{}, nr.carriers.ExcludeItemTypes...), musicItemTypes...)
	}
	ctx := r.Context()
	q := nr.q
	nq := storage.NamedQuery{
		Kind:                    kind,
		Scope:                   nr.carriers,
		SearchTerm:              strings.TrimSpace(q.Get("searchterm")),
		NameStartsWith:          q.Get("namestartswith"),
		NameStartsWithOrGreater: q.Get("namestartswithorgreater"),
		NameLessThan:            q.Get("namelessthan"),
		PersonTypes:             itemsListParam(q, "persontypes"),
		ExcludePersonTypes:      itemsListParam(q, "excludepersontypes"),
	}
	if raw := strings.TrimSpace(q.Get("appearsinitemid")); raw != "" {
		g, valid := normalizeGUID(raw)
		if !valid {
			writeInvalidID(w, "appearsInItemId", raw)
			return
		}
		if g != zeroGUID {
			id := storage.ItemID(g)
			nq.AppearsIn = &id
		}
	}
	// Sort: only the name is a sort key here; the default order is
	// alphabetical for people, genres and studios, first appearance for
	// years.
	for _, tok := range itemsListParam(q, "sortby") {
		if strings.EqualFold(tok, "SortName") || strings.EqualFold(tok, "Name") {
			nq.ByName = true
		}
	}
	for _, tok := range itemsListParam(q, "sortorder") {
		if strings.EqualFold(tok, "Descending") {
			nq.Descending = true
		}
	}
	if nr.iq.filter.Favorite != nil && *nr.iq.filter.Favorite {
		// Favorites of named items are not stored.
		nr.noMatch = true
	}
	if nr.noMatch || nr.iq.filter.NoMatch {
		writeJSON(w, http.StatusOK, namedItemsResult{Items: []any{}, StartIndex: nr.iq.startIndex})
		return
	}
	if nr.iq.limitGiven || nr.iq.limit != unboundedLimit {
		nq.Limit = nr.iq.limit
	}
	nq.Offset = nr.iq.startIndex
	page, err := s.store.QueryNamed(ctx, nq)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// The counts are of everything that carries the name, whatever item
	// types the listing was narrowed to by IncludeItemTypes (an exclusion
	// does narrow them).
	counts := map[string]storage.NamedEntry{}
	if len(nq.Scope.ItemTypes) > 0 && !ns.music && (kind == storage.NamedGenre || kind == storage.NamedStudio) {
		all := nq
		all.Scope.ItemTypes = nil
		all.Limit, all.Offset = 0, 0
		if full, err := s.store.QueryNamed(ctx, all); err == nil {
			for _, e := range full.Entries {
				counts[strings.ToLower(e.Name)] = e
			}
		}
	}
	items := make([]any, 0, len(page.Entries))
	for _, e := range page.Entries {
		if c, ok := counts[strings.ToLower(e.Name)]; ok {
			e.Movies, e.Series, e.Episodes, e.Playlists, e.Others, e.Total = c.Movies, c.Series, c.Episodes, c.Playlists, c.Others, c.Total
		}
		d := s.namedItemDto(e, namedDtoOpts{userData: withUserData && nr.iq.userData, counts: true})
		d = shapeNamed(d, nr.iq, kind, ns.music)
		if ns.music {
			d.Type = "MusicGenre"
			d.Path = ""
			if nr.iq.fields["path"] {
				d.Path = path.Join("/metadata/MusicGenre", e.Name)
			}
		}
		items = append(items, d)
	}
	total := page.Total
	if (kind == storage.NamedGenre || kind == storage.NamedStudio) && strings.EqualFold(nr.iq.totalRecord, "false") {
		total = 0 // these two listings report no total when it is not asked for
	}
	writeJSON(w, http.StatusOK, namedItemsResult{
		Items:            items,
		TotalRecordCount: total,
		StartIndex:       nr.iq.startIndex,
	})
}

func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	s.listNamedIn(w, r, storage.NamedGenre, false, namedScope{noMusic: true})
}

// handleMusicGenres is GET /MusicGenres: the genres of audio, albums and
// artists, with the same shape as /Genres but type MusicGenre.
func (s *Server) handleMusicGenres(w http.ResponseWriter, r *http.Request) {
	s.listNamedIn(w, r, storage.NamedGenre, false, namedScope{music: true})
}

func (s *Server) handleStudios(w http.ResponseWriter, r *http.Request) {
	s.listNamed(w, r, storage.NamedStudio, true)
}

func (s *Server) handlePersons(w http.ResponseWriter, r *http.Request) {
	s.listNamed(w, r, storage.NamedPerson, true)
}

func (s *Server) handleYears(w http.ResponseWriter, r *http.Request) {
	s.listNamed(w, r, storage.NamedYear, true)
}

// namedSingle serves GET /Genres/{name}, /Studios/{name}, /Persons/{name}
// and /Years/{year}: the named item with the counts of the items the caller
// can see that carry it. Genres, studios and years answer for a name nothing
// carries (with zero counts); a person nothing credits is a 404.
func (s *Server) namedSingle(w http.ResponseWriter, r *http.Request, kind storage.NamedKind, name string) {
	s.namedSingleIn(w, r, kind, name, namedScope{})
}

func (s *Server) namedSingleIn(w http.ResponseWriter, r *http.Request, kind storage.NamedKind, name string, ns namedScope) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	user, ok := s.itemsContextUser(w, r, q.Get("userid"))
	if !ok {
		return
	}
	sc, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var scope storage.ItemFilter
	sc.apply(&scope)
	if ns.music {
		scope.ItemTypes = musicItemTypes
	} else if ns.noMusic {
		scope.ExcludeItemTypes = append(append([]storage.ItemType{}, scope.ExcludeItemTypes...), musicItemTypes...)
	}
	ref := storage.NamedRef{Kind: kind, Name: name, ID: storage.NamedID(kind, name)}
	if known, found, _ := s.store.FindNamed(ctx, ref.ID); found {
		ref.Name = known.Name // the spelling the catalog carries
	}
	e, err := s.store.CountNamed(ctx, ref, scope)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if kind == storage.NamedPerson && e.Total == 0 {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	dto := s.namedItemDto(e, namedDtoOpts{full: true, userData: true, counts: true})
	if ns.music {
		dto.Type = "MusicGenre"
		dto.Path = path.Join("/metadata/MusicGenre", e.Name)
		if dto.UserData != nil {
			dto.UserData.Key = "MusicGenre-" + e.Name
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleGenreByName(w http.ResponseWriter, r *http.Request) {
	s.namedSingleIn(w, r, storage.NamedGenre, r.PathValue("genreName"), namedScope{noMusic: true})
}

func (s *Server) handleMusicGenreByName(w http.ResponseWriter, r *http.Request) {
	s.namedSingleIn(w, r, storage.NamedGenre, r.PathValue("genreName"), namedScope{music: true})
}

func (s *Server) handleStudioByName(w http.ResponseWriter, r *http.Request) {
	s.namedSingle(w, r, storage.NamedStudio, r.PathValue("name"))
}

func (s *Server) handlePersonByName(w http.ResponseWriter, r *http.Request) {
	s.namedSingle(w, r, storage.NamedPerson, r.PathValue("name"))
}

func (s *Server) handleYearByNumber(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("year")
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeInvalidID(w, "year", raw)
		return
	}
	s.namedSingle(w, r, storage.NamedYear, strconv.Itoa(n))
}

// namedItemByID resolves an identifier that names no stored item or library
// to a named item. ok is false when it names none.
func (s *Server) namedItemByID(ctx context.Context, sc itemsScope, id string) (namedDto, bool) {
	ref, found, err := s.store.FindNamed(ctx, storage.ItemID(id))
	if err != nil || !found {
		return namedDto{}, false
	}
	var scope storage.ItemFilter
	sc.apply(&scope)
	e, err := s.store.CountNamed(ctx, ref, scope)
	if err != nil {
		return namedDto{}, false
	}
	if e.Total == 0 && ref.Kind == storage.NamedPerson {
		return namedDto{}, false
	}
	return s.namedItemDto(e, namedDtoOpts{full: true, userData: true, counts: true}), true
}

// resolveNamedIDs maps identifiers of named items of one kind to their
// names; identifiers that name nothing are dropped.
func (s *Server) resolveNamedIDs(ctx context.Context, kind storage.NamedKind, tokens []string) (names []string, any bool) {
	for _, tok := range tokens {
		g, ok := normalizeGUID(tok)
		if !ok {
			continue
		}
		any = true
		if ref, found, err := s.store.FindNamed(ctx, storage.ItemID(g)); err == nil && found && ref.Kind == kind {
			names = append(names, ref.Name)
		}
	}
	return names, any
}

// legacyFilters is the body of GET /Items/Filters.
type legacyFilters struct {
	Genres          []string `json:"Genres"`
	Tags            []string `json:"Tags"`
	OfficialRatings []string `json:"OfficialRatings"`
	Years           []int    `json:"Years"`
}

// filterFacets collects the distinct genres, tags, ratings and years of the
// items in scope (one bounded pass over the matching rows, no cache).
func (s *Server) filterFacets(ctx context.Context, scope storage.ItemFilter) (genres []storage.NamedEntry, tags, ratings []string, years []int, err error) {
	q := storage.NamedQuery{Kind: storage.NamedGenre, Scope: scope, ByName: true}
	gp, err := s.store.QueryNamed(ctx, q)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	genres = gp.Entries
	q.Kind = storage.NamedYear
	yp, err := s.store.QueryNamed(ctx, q)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, e := range yp.Entries {
		if n, err := strconv.Atoi(e.Name); err == nil {
			years = append(years, n)
		}
	}
	sort.Ints(years)
	f := scope
	f.Limit = 5000
	page, err := s.store.QueryItems(ctx, f)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	seenT, seenR := map[string]bool{}, map[string]bool{}
	for _, it := range page.Items {
		for _, t := range it.Tags {
			if k := strings.ToLower(t); !seenT[k] {
				seenT[k] = true
				tags = append(tags, t)
			}
		}
		if it.OfficialRating != "" && !seenR[it.OfficialRating] {
			seenR[it.OfficialRating] = true
			ratings = append(ratings, it.OfficialRating)
		}
	}
	sort.Strings(tags)
	sort.Strings(ratings)
	return genres, tags, ratings, years, nil
}

// filtersScope is the carrier scope of the facet routes: the caller's
// libraries, narrowed by ParentId and (Filters2) the item types.
func (s *Server) filtersScope(w http.ResponseWriter, r *http.Request, useTypes bool) (storage.ItemFilter, bool, bool) {
	nr, ok := s.newNamedReq(w, r)
	if !ok {
		return storage.ItemFilter{}, false, false
	}
	f := nr.carriers
	if !useTypes {
		f.ItemTypes, f.ExcludeItemTypes = nil, nil
	}
	if nr.noMatch {
		f.NoMatch = true
	}
	// A flag only live TV programs carry matches nothing here.
	for _, key := range []string{"isseries", "iskids", "isnews", "issports", "isairing"} {
		if v := strings.ToLower(strings.TrimSpace(nr.q.Get(key))); v == "true" {
			f.NoMatch = true
		}
	}
	return f, len(f.ItemTypes) > 0, true
}

// handleItemsFilters is GET /Items/Filters (legacy facets): empty unless a
// ParentId is given.
func (s *Server) handleItemsFilters(w http.ResponseWriter, r *http.Request) {
	f, _, ok := s.filtersScope(w, r, false)
	if !ok {
		return
	}
	out := legacyFilters{Genres: []string{}, Tags: []string{}, OfficialRatings: []string{}, Years: []int{}}
	if strings.TrimSpace(caseInsensitiveQuery(r).Get("parentid")) != "" && !f.NoMatch {
		genres, tags, ratings, years, err := s.filterFacets(r.Context(), f)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, g := range genres {
			out.Genres = append(out.Genres, g.Name)
		}
		if tags != nil {
			out.Tags = tags
		}
		if ratings != nil {
			out.OfficialRatings = ratings
		}
		if years != nil {
			out.Years = years
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// filters2 is the body of GET /Items/Filters2.
type filters2 struct {
	Genres            []nameIDPairDto `json:"Genres"`
	Tags              []string        `json:"Tags"`
	AudioLanguages    []nameValueDto  `json:"AudioLanguages"`
	SubtitleLanguages []nameValueDto  `json:"SubtitleLanguages"`
}

type nameValueDto struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// handleItemsFilters2 is GET /Items/Filters2: the genres (with ids) and tags
// of the items in scope, narrowed by ParentId and IncludeItemTypes.
func (s *Server) handleItemsFilters2(w http.ResponseWriter, r *http.Request) {
	f, typed, ok := s.filtersScope(w, r, true)
	if !ok {
		return
	}
	out := filters2{Genres: []nameIDPairDto{}, Tags: []string{}, AudioLanguages: []nameValueDto{}, SubtitleLanguages: []nameValueDto{}}
	if !f.NoMatch {
		genres, tags, _, _, err := s.filterFacets(r.Context(), f)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, g := range genres {
			out.Genres = append(out.Genres, nameIDPairDto{Name: g.Name, ID: string(g.ID)})
		}
		if tags != nil {
			out.Tags = tags
		}
		if typed {
			langs, err := s.audioLanguages(r.Context(), f)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			out.AudioLanguages = langs
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// audioLanguages lists the audio languages of the playable items in scope
// ("und" for a track with no language), sorted by code.
func (s *Server) audioLanguages(ctx context.Context, scope storage.ItemFilter) ([]nameValueDto, error) {
	f := scope
	f.Limit = 5000
	page, err := s.store.QueryItems(ctx, f)
	if err != nil {
		return nil, err
	}
	items := page.Items
	// A series or season stands for its episodes' tracks.
	for _, t := range scope.ItemTypes {
		if t == storage.ItemTypeSeries || t == storage.ItemTypeSeason {
			ef := scope
			ef.ItemTypes = []storage.ItemType{storage.ItemTypeEpisode}
			ef.Recursive = true
			ef.Limit = 5000
			ep, err := s.store.QueryItems(ctx, ef)
			if err != nil {
				return nil, err
			}
			items = append(items, ep.Items...)
			break
		}
	}
	seen := map[string]bool{}
	var codes []string
	for _, it := range items {
		for _, st := range it.MediaStreams {
			if st.Type != storage.StreamTypeAudio {
				continue
			}
			code := strings.ToLower(st.Language)
			if code == "" {
				code = "und"
			}
			if !seen[code] {
				seen[code] = true
				codes = append(codes, code)
			}
		}
	}
	sort.Strings(codes)
	out := make([]nameValueDto, 0, len(codes))
	for _, c := range codes {
		name := c
		if c == "und" {
			name = "Undetermined (und)"
		}
		out = append(out, nameValueDto{Name: name, Value: c})
	}
	return out, nil
}
