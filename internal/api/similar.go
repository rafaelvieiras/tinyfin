// similar.go implements the discovery routes (docs/contract/specs/browse.md):
//
//	GET /Items/{itemId}/Similar (and the /Movies, /Shows, /Trailers forms)
//	GET /Movies/Recommendations
//	GET /Items/Suggestions
//	GET /Items/{itemId}/Intros
//	GET /Items/{itemId}/Collections
//
// None of them keeps state: similarity is the number of genres, studios,
// people and tags two items share, recommendations come from what the user
// played last, suggestions are a random sample of what the user can see.
package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// defaultSimilarLimit is how many similar items come back when Limit is not
// given.
const defaultSimilarLimit = 10

// similarSpecFor is the set of values an item is compared on.
func similarSpecFor(it storage.Item) *storage.SimilarSpec {
	sp := &storage.SimilarSpec{Genres: it.Genres, Studios: it.Studios, Tags: it.Tags}
	for _, p := range it.People {
		sp.People = append(sp.People, p.Name)
	}
	return sp
}

// similarKinds are the item types that have a "more like this" list.
func similarKind(t storage.ItemType) bool {
	return t == storage.ItemTypeMovie || t == storage.ItemTypeSeries ||
		t == storage.ItemTypeAudio || t == storage.ItemTypeMusicAlbum || t == storage.ItemTypeMusicArtist
}

// loadReadableItem resolves {itemId} for the discovery routes: a non-GUID is
// a validation problem, a missing or unreadable item a 404 problem.
func (s *Server) loadReadableItem(w http.ResponseWriter, r *http.Request) (storage.Item, itemsScope, bool) {
	raw := r.PathValue("itemId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "itemId", raw)
		return storage.Item{}, itemsScope{}, false
	}
	ctx := r.Context()
	sc, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return storage.Item{}, itemsScope{}, false
	}
	item, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil || !s.itemReadable(ctx, sc, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return storage.Item{}, itemsScope{}, false
	}
	return item, sc, true
}

// discoveryQuery binds Fields/EnableUserData/... and the paging of a
// discovery route.
func discoveryQuery(r *http.Request) (itemsQuery, bool) {
	iq, err := parseItemsQuery(caseInsensitiveQuery(r), false)
	return iq, err == nil
}

// handleItemSimilar serves GET /Items/{itemId}/Similar and the type-named
// aliases: the items of the same type that share the most genres, studios,
// people and tags with the item, most shared first, never the item itself.
func (s *Server) handleItemSimilar(w http.ResponseWriter, r *http.Request) {
	item, sc, ok := s.loadReadableItem(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	limit, err := itemsInt(q, "limit")
	if err != nil {
		writeValidationProblem(w, "limit", "The value '"+q.Get("limit")+"' is not valid.")
		return
	}
	iq, ok2 := discoveryQuery(r)
	if !ok2 {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	out := []baseItemDto{}
	n := defaultSimilarLimit
	if limit != nil {
		n = *limit
	}
	if item.Type == storage.ItemTypeMusicArtist && n > 0 {
		items, err := s.similarArtists(ctx, item, sc, n)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, it := range items {
			out = append(out, shapeListItem(s.baseItemDto(ctx, it), iq))
		}
	} else if similarKind(item.Type) && n > 0 {
		f := storage.ItemFilter{
			ItemTypes:  []storage.ItemType{item.Type},
			ExcludeIDs: []storage.ItemID{item.ID},
			Similar:    similarSpecFor(item),
			Recursive:  true,
			Limit:      n,
		}
		if (item.Type == storage.ItemTypeAudio || item.Type == storage.ItemTypeMusicAlbum) && len(item.Genres) == 0 {
			// A track or album with no genre is similar to every other one.
			f.Similar = nil
		}
		sc.apply(&f)
		page, err := s.store.QueryItems(ctx, f)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, it := range page.Items {
			out = append(out, shapeListItem(s.baseItemDto(ctx, it), iq))
		}
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: out, TotalRecordCount: len(out)})
}

// similarArtists lists the artists the caller can see that share genres
// with it, most shared first. An artist's genres are those of its artist.nfo,
// or else of the tracks that name it (music.artist.R4).
func (s *Server) similarArtists(ctx context.Context, item storage.Item, sc itemsScope, limit int) ([]storage.Item, error) {
	tf := storage.ItemFilter{Recursive: true, ItemTypes: []storage.ItemType{storage.ItemTypeAudio}, Limit: 100000}
	sc.apply(&tf)
	tracks, err := s.store.QueryItems(ctx, tf)
	if err != nil {
		return nil, err
	}
	fromTracks := map[string]map[string]bool{}
	add := func(name string, genres []string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		set := fromTracks[name]
		if set == nil {
			set = map[string]bool{}
			fromTracks[name] = set
		}
		for _, g := range genres {
			set[strings.ToLower(g)] = true
		}
	}
	for _, t := range tracks.Items {
		add(t.AlbumArtist, t.Genres)
		for _, a := range t.Artists {
			if !strings.EqualFold(a, t.AlbumArtist) {
				add(a, t.Genres)
			}
		}
	}
	genresOf := func(a storage.Item) map[string]bool {
		if len(a.Genres) == 0 {
			return fromTracks[strings.ToLower(a.Name)]
		}
		set := map[string]bool{}
		for _, g := range a.Genres {
			set[strings.ToLower(g)] = true
		}
		return set
	}
	mine := genresOf(item)
	af := storage.ItemFilter{Recursive: true, IncludeExtras: true,
		ItemTypes: []storage.ItemType{storage.ItemTypeMusicArtist}, ExcludeIDs: []storage.ItemID{item.ID},
		SortBy: []storage.SortField{{By: storage.SortByName}}, Limit: 100000}
	sc.apply(&af)
	cands, err := s.store.QueryItems(ctx, af)
	if err != nil {
		return nil, err
	}
	type scored struct {
		it storage.Item
		n  int
	}
	var ranked []scored
	for _, c := range cands.Items {
		if c.ExtraType != "" && c.ExtraType != storage.ExtraTypeVirtualArtist {
			continue
		}
		n := 0
		for g := range genresOf(c) {
			if mine[g] {
				n++
			}
		}
		// An artist with no genre at all is similar to every artist.
		if n > 0 || len(mine) == 0 {
			ranked = append(ranked, scored{c, n})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].n > ranked[j].n })
	var out []storage.Item
	for _, r := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, r.it)
	}
	return out, nil
}

// handleItemIntros serves GET /Items/{itemId}/Intros and the per-user form:
// pre-roll items played before the item. None exist here.
func (s *Server) handleItemIntros(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.loadReadableItem(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: []baseItemDto{}})
}

// handleItemCollections serves GET /Items/{itemId}/Collections: the
// collections the item is a direct member of, as far as the caller can see
// them.
func (s *Server) handleItemCollections(w http.ResponseWriter, r *http.Request) {
	item, sc, ok := s.loadReadableItem(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	iq, ok2 := discoveryQuery(r)
	if !ok2 {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	rels, err := s.store.QueryRelations(ctx, item.ID, storage.RelationCollection, false)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var ids []storage.ItemID
	for _, rel := range rels {
		ids = append(ids, rel.ParentID)
	}
	out := []baseItemDto{}
	total := 0
	if len(ids) > 0 {
		f := storage.ItemFilter{IDs: ids, ItemTypes: []storage.ItemType{storage.ItemTypeBoxSet}, Limit: 1000}
		sc.apply(&f)
		page, err := s.store.QueryItems(ctx, f)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		total = page.TotalCount
		start := iq.startIndex
		if start > len(page.Items) {
			start = len(page.Items)
		}
		items := page.Items[start:]
		if iq.limitGiven && iq.limit < len(items) {
			items = items[:iq.limit]
		}
		for _, it := range items {
			out = append(out, shapeListItem(s.baseItemDto(ctx, it), iq))
		}
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: out, TotalRecordCount: total, StartIndex: iq.startIndex})
}

// recommendationDto mirrors the reference's RecommendationDto.
type recommendationDto struct {
	Items              []baseItemDto `json:"Items"`
	RecommendationType string        `json:"RecommendationType"`
	BaselineItemName   string        `json:"BaselineItemName"`
	CategoryID         string        `json:"CategoryId"`
}

// handleMovieRecommendations serves GET /Movies/Recommendations. Categories
// come from the movies the user played last: movies similar to one, movies
// by a director of one, movies with an actor of one. Items are movies the
// user has not played; a user who played nothing gets no categories.
func (s *Server) handleMovieRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	user, ok := s.itemsContextUser(w, r, q.Get("userid"))
	if !ok {
		return
	}
	catLimit, err := itemsInt(q, "categorylimit")
	if err != nil {
		writeValidationProblem(w, "categoryLimit", "The value '"+q.Get("categorylimit")+"' is not valid.")
		return
	}
	itemLimit, err := itemsInt(q, "itemlimit")
	if err != nil {
		writeValidationProblem(w, "itemLimit", "The value '"+q.Get("itemlimit")+"' is not valid.")
		return
	}
	nCat, nItem := 5, 8
	if catLimit != nil {
		nCat = *catLimit
	}
	if itemLimit != nil {
		nItem = *itemLimit
	}
	iq, ok2 := discoveryQuery(r)
	if !ok2 {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
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
	base := storage.ItemFilter{Recursive: true}
	if raw := strings.TrimSpace(q.Get("parentid")); raw != "" {
		g, valid := normalizeGUID(raw)
		if !valid {
			writeInvalidID(w, "parentId", raw)
			return
		}
		if lib, err := s.store.GetLibrary(ctx, storage.LibraryID(g)); err == nil {
			id := lib.ID
			base.LibraryID = &id
		} else if it, err := s.store.GetItem(ctx, storage.ItemID(g)); err == nil {
			id := it.ID
			base.ParentID = &id
		} else {
			base.NoMatch = true
		}
	}
	out := []recommendationDto{}
	write := func() {
		if len(out) == 0 {
			out = []recommendationDto{}
		}
		writeJSON(w, http.StatusOK, out)
	}
	if nCat <= 0 || base.NoMatch {
		write()
		return
	}

	// The user's last played movies, newest first.
	uid := user.ID
	played := true
	pf := base
	pf.ItemTypes = []storage.ItemType{storage.ItemTypeMovie}
	pf.UserID, pf.Played = &uid, &played
	pf.SortBy = []storage.SortField{{By: storage.SortByDateCreated, Descending: true}}
	pf.Limit = 20
	sc.apply(&pf)
	recent, err := s.store.QueryItems(ctx, pf)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	unplayed := false
	candidates := func(extra func(*storage.ItemFilter)) ([]baseItemDto, error) {
		f := base
		f.ItemTypes = []storage.ItemType{storage.ItemTypeMovie}
		f.UserID, f.Played = &uid, &unplayed
		f.Limit = nItem
		extra(&f)
		sc.apply(&f)
		page, err := s.store.QueryItems(ctx, f)
		if err != nil {
			return nil, err
		}
		items := make([]baseItemDto, 0, len(page.Items))
		for _, it := range page.Items {
			items = append(items, shapeListItem(s.baseItemDto(ctx, it), iq))
		}
		return items, nil
	}
	usedPeople := map[string]bool{}
	for _, m := range recent.Items {
		if len(out) >= nCat {
			break
		}
		items, err := candidates(func(f *storage.ItemFilter) {
			f.Similar = similarSpecFor(m)
			f.ExcludeIDs = []storage.ItemID{m.ID}
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if len(items) > 0 {
			out = append(out, recommendationDto{Items: items, RecommendationType: "SimilarToRecentlyPlayed", BaselineItemName: m.Name, CategoryID: string(m.ID)})
		}
		for _, kind := range []struct{ credit, rtype string }{
			{"Director", "HasDirectorFromRecentlyPlayed"}, {"Actor", "HasActorFromRecentlyPlayed"},
		} {
			if len(out) >= nCat {
				break
			}
			for _, p := range m.People {
				if p.Type != kind.credit || usedPeople[kind.credit+":"+strings.ToLower(p.Name)] {
					continue
				}
				usedPeople[kind.credit+":"+strings.ToLower(p.Name)] = true
				name := p.Name
				items, err := candidates(func(f *storage.ItemFilter) {
					f.PersonNames = []string{name}
					f.PersonTypes = []string{kind.credit}
					f.ExcludeIDs = []storage.ItemID{m.ID}
				})
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if len(items) > 0 {
					out = append(out, recommendationDto{Items: items, RecommendationType: kind.rtype, BaselineItemName: p.Name, CategoryID: personID(p.Name)})
				}
				break
			}
		}
	}
	write()
}

// handleItemsSuggestions serves GET /Items/Suggestions: a random sample of
// the playable items (movies and series by default, or the requested types)
// the user can see.
func (s *Server) handleItemsSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
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
	var types []storage.ItemType
	for _, tok := range itemsListParam(q, "type") {
		if k, ok := itemsKnownKinds[strings.ToLower(tok)]; ok {
			types = append(types, k)
		}
	}
	if len(types) == 0 {
		types = []storage.ItemType{storage.ItemTypeMovie, storage.ItemTypeSeries}
	}
	f := storage.ItemFilter{ItemTypes: types, Recursive: true, SortBy: []storage.SortField{{By: storage.SortByRandom}}, Limit: 12}
	if limit != nil {
		f.Limit = *limit
	}
	if start != nil && *start > 0 {
		f.Offset = *start
	}
	if mt := itemsListParam(q, "mediatype"); len(mt) > 0 {
		var kinds []storage.ItemType
		for _, m := range mt {
			kinds = append(kinds, mediaTypeKinds[strings.ToLower(m)]...)
		}
		var keep []storage.ItemType
		for _, t := range f.ItemTypes {
			for _, k := range kinds {
				if t == k {
					keep = append(keep, t)
				}
			}
		}
		f.ItemTypes = keep
		if len(keep) == 0 {
			f.NoMatch = true
		}
	}
	iq, ok2 := discoveryQuery(r)
	if !ok2 {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	sc.apply(&f)
	countOnly := f.Limit <= 0
	if countOnly {
		f.Limit = 1
	}
	page, err := s.store.QueryItems(ctx, f)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]baseItemDto, 0, len(page.Items))
	if !countOnly {
		for _, it := range page.Items {
			items = append(items, shapeListItem(s.baseItemDto(ctx, it), iq))
		}
	}
	total := resolveTotalRecordCount(q.Get("enabletotalrecordcount"), len(items), page.TotalCount)
	if limit != nil {
		total = len(items) // a limit makes the total the size of the page
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: total,
		StartIndex:       f.Offset,
	})
}
