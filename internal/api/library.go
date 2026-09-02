// library.go implements the "library and item navigation" group from
// docs/04 (its highest-priority group): user views, item listing/query,
// item detail, image bytes, and the home-screen/detail-page widgets layered
// on top (Latest, Resume, Ancestors, Similar, Filters, Studios). Playback
// negotiation and the actual media-file stream live in playback.go.
//
// Found missing as a group, not one at a time, while investigating a
// "several fields come back null / lots of 404s in the dashboard" report:
// several of these (Latest, Resume, Ancestors) are real navigation gaps with
// data already sitting in storage and nothing serving it; others
// (Similar, Filters/Filters2, Studios) have no real data behind them yet
// (no similarity algorithm, no genre/tag/studio scanning — that's
// metadata-provider-adjacent, 08) but are registered anyway as honest-empty
// responses rather than left 404ing, because several client call sites for
// them render their section unconditionally before the request resolves
// (see handleItemSimilar's doc comment) — a 404 there is a permanently
// broken-looking section, not just a missing feature.
//
// Still a deliberate gap, not silently expanded into: suggestions/
// instant-mix, and collections/playlists CRUD beyond what QueryItems
// already exposes read-only.
package api

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// defaultItemsLimit bounds GET /Items when the client doesn't specify its
// own limit. storage.ItemFilter.Limit is required and rejected if <= 0
// (docs/05, item.go) specifically so no caller can accidentally
// materialize a full catalog — this is where that "never pass an
// unbounded limit through" rule gets enforced on the HTTP side (AGENTS.md
// principle 1).
const defaultItemsLimit = 100

// handleUserViews serves GET /UserViews: the library roots, one BaseItemDto
// per storage.Library. QueryLibraries is intentionally unbounded (docs/05:
// library count is operator-scale, not catalog-scale, same reasoning as
// UserStore.QueryUsers), so no paging is applied here.
func (s *Server) handleUserViews(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	items := make([]baseItemDto, 0, len(libs))
	for _, lib := range libs {
		items = append(items, s.libraryBaseItemDto(r.Context(), lib))
	}

	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: len(items),
		StartIndex:       0,
	})
}

// sortByValues maps the wire-level ItemSortBy strings (docs/04) this
// project exposes to storage.SortBy constants (storage/item.go). Only the
// values QueryItems callers actually use today have constants — an
// unrecognized sortBy token is silently skipped rather than erroring,
// matching how the reference server tolerates being asked to sort by a
// key it doesn't index.
var sortByValues = map[string]storage.SortBy{
	"SortName":          storage.SortByName,
	"DateCreated":       storage.SortByDateCreated,
	"PremiereDate":      storage.SortByPremiereDate,
	"ProductionYear":    storage.SortByProductionYear,
	"CommunityRating":   storage.SortByCommunityRating,
	"Random":            storage.SortByRandom,
	"IndexNumber":       storage.SortByIndexNumber,
	"ParentIndexNumber": storage.SortByParentIndexNumber,
	"SeriesSortName":    storage.SortBySeriesName,
}

// parseSortFields maps the comma-separated sortBy/sortOrder query params
// (docs/04) to []storage.SortField. A single sortOrder value applies to
// every sortBy key; otherwise sortOrder entries pair positionally with
// sortBy entries, matching the reference server's own convention for
// multi-key sorts (e.g. "sort by ParentIndexNumber then IndexNumber" for
// an episode listing, per storage/item.go's SortField doc comment).
func parseSortFields(sortBy, sortOrder string) []storage.SortField {
	if sortBy == "" {
		return nil
	}
	names := strings.Split(sortBy, ",")
	orders := strings.Split(sortOrder, ",")

	fields := make([]storage.SortField, 0, len(names))
	for i, n := range names {
		by, ok := sortByValues[strings.TrimSpace(n)]
		if !ok {
			continue
		}
		var order string
		switch {
		case len(orders) == 1:
			order = orders[0]
		case i < len(orders):
			order = orders[i]
		}
		fields = append(fields, storage.SortField{
			By:         by,
			Descending: strings.EqualFold(strings.TrimSpace(order), "Descending"),
		})
	}
	return fields
}

// caseInsensitiveQuery returns r.URL.Query() with every key lowercased, so a
// caller can look values up with a single lowercase q.Get(...) regardless of
// how the client capitalized the key. url.Values.Get is case-sensitive, but
// real clients disagree on capitalization across call sites: most of this
// project's params were derived from lowercase-first calls (parentId,
// searchTerm, sortBy, ...), but jellyfin-web's Favorites screen
// (#/home?tab=1) sends the same keys PascalCase (IncludeItemTypes,
// Recursive, SortBy, Filters, ...) — confirmed via Playwright network
// capture against a real jellyfin-web build, not assumed. Scoped to
// handleItemsList only (the one handler this capitalization mismatch was
// observed breaking) rather than applied project-wide, to keep this fix
// local to what the Favorites screen actually needs — every other handler
// in this file still reads params in the lowercase-first form real clients
// have been observed sending them in for those specific routes.
func caseInsensitiveQuery(r *http.Request) url.Values {
	orig := r.URL.Query()
	out := make(url.Values, len(orig))
	for k, v := range orig {
		out[strings.ToLower(k)] = v
	}
	return out
}

// resolveTotalRecordCount picks what a QueryResult's TotalRecordCount field
// should actually say: the real total behind the filter (enableParam absent,
// or anything but exactly "false" — the default), or just how many items
// this particular response carries. Confirmed against a real Jellyfin
// server (staging, :8097), not assumed: favoriting 8 movies and requesting
// Limit=5&EnableTotalRecordCount=false came back with TotalRecordCount 5,
// not 8 — the real total the same request with the param left off (or set
// to true) does return. A second page (StartIndex=5) of the same query
// came back with TotalRecordCount 3, matching that page's own item count,
// not len(Items) clamped some other way — confirming it's genuinely
// "however many are in this response," the cheap count the flag exists to
// let the server skip a separate COUNT(*) for, not real-total-minus-offset
// or any other derived value. jellyfin-web's Favorites screen (#/home?tab=1)
// and "Continue Watching" row both send this param as false on every call.
func resolveTotalRecordCount(enableParam string, itemCount, realTotal int) int {
	if strings.EqualFold(enableParam, "false") {
		return itemCount
	}
	return realTotal
}

// handleItemsList serves both GET /Items and GET /Users/{userId}/Items (the
// per-user listing form jellyfin-web's Favorites screen calls — {userId} is
// accepted but ignored, same precedent as handleItemGet's per-user form
// below; see server.go's registration comment). Maps the subset of query
// params this slice covers (parentId, recursive, includeItemTypes,
// searchTerm, limit/startIndex, sortBy/sortOrder, filters) to a
// storage.ItemFilter and returns a BaseItemDtoQueryResult. Filters beyond
// IsFavorite/IsPlayed (genres, years, ...) are a deliberate gap, not a
// silent guess — left for a later increment, not something the Favorites
// screen this was added for needs.
func (s *Server) handleItemsList(w http.ResponseWriter, r *http.Request) {
	q := caseInsensitiveQuery(r)

	filter := storage.ItemFilter{
		Recursive:  q.Get("recursive") == "true",
		SearchText: q.Get("searchterm"),
		Limit:      defaultItemsLimit,
	}

	if v := q.Get("filters"); v != "" {
		var wantsPlaystate bool
		for _, f := range strings.Split(v, ",") {
			switch strings.TrimSpace(f) {
			case "IsFavorite":
				t := true
				filter.Favorite = &t
				wantsPlaystate = true
			case "IsPlayed":
				t := true
				filter.Played = &t
				wantsPlaystate = true
			}
			// Other Filters values the reference server defines (IsNotFavorite,
			// IsUnplayed, ...) aren't observed being sent by the Favorites
			// screen this was added for — deliberately not guessed at, see
			// this function's doc comment.
		}
		// storage.ItemFilter.Played/Favorite only take effect when UserID is
		// set (types.go's doc comment: they key off a LEFT JOIN against that
		// user's playstate) — this route has no other reason to set UserID,
		// so only do it when a playstate filter actually needs it.
		if wantsPlaystate {
			if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
				userID := ac.User.ID
				filter.UserID = &userID
			}
		}
	}

	if v := q.Get("parentid"); v != "" {
		// A library root isn't itself an Item row in this storage model
		// (storage.Library and storage.Item are separate, see
		// storage/library.go) — GET /UserViews hands clients a Library's
		// ID as a browsable "parent". Try that first; anything else is an
		// ordinary Item parent (e.g. a season browsing its episodes).
		//
		// Known simplification: filtering by LibraryID returns the whole
		// library subtree regardless of the recursive flag (Item.LibraryID
		// doesn't encode nesting depth, per storage/item.go's doc
		// comment) — a non-recursive top-level listing of a library root
		// isn't distinguishable from a recursive one here.
		if _, err := s.store.GetLibrary(r.Context(), storage.LibraryID(v)); err == nil {
			libID := storage.LibraryID(v)
			filter.LibraryID = &libID
		} else {
			id := storage.ItemID(v)
			filter.ParentID = &id
		}
	}

	if v := q.Get("includeitemtypes"); v != "" {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filter.ItemTypes = append(filter.ItemTypes, storage.ItemType(t))
			}
		}
	}

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
		// A non-numeric or non-positive limit falls back to
		// defaultItemsLimit rather than erroring or passing an unbounded
		// value through to storage.
	}

	if v := q.Get("startindex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}

	filter.SortBy = parseSortFields(q.Get("sortby"), q.Get("sortorder"))

	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, s.baseItemDto(r.Context(), it))
	}

	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: resolveTotalRecordCount(q.Get("enabletotalrecordcount"), len(items), page.TotalCount),
		StartIndex:       page.Offset,
	})
}

// handleItemGet serves GET /Items/{itemId}.
//
// A library root isn't itself an Item row (storage.Library and storage.Item
// are separate stores, storage/library.go's doc comment) — GET /UserViews
// hands clients a Library's ID as a browsable item, same as handleItemsList's
// parentId handling above, so a plain GetItem miss falls back to GetLibrary
// before 404ing. Found by testing against a real jellyfin-android build:
// its WebView item-detail flow calls this route with a library's own ID
// (reached by tapping a UserView tile straight through to its "item" view)
// and 404ing there is a dead end the client doesn't recover from.
func (s *Server) handleItemGet(w http.ResponseWriter, r *http.Request) {
	id := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		if lib, libErr := s.store.GetLibrary(r.Context(), storage.LibraryID(id)); libErr == nil {
			writeJSON(w, http.StatusOK, s.libraryBaseItemDto(r.Context(), lib))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.baseItemDto(r.Context(), item))
}

// defaultLatestLimit is GET /Items/Latest's own default page size when the
// client doesn't specify limit — distinct from defaultItemsLimit (GET
// /Items), matching the reference server's own smaller default for this
// specific "recently added" widget.
const defaultLatestLimit = 20

// handleItemsLatest serves GET /Items/Latest — the home screen's "Recently
// Added" row. Unlike GET /Items, the reference server returns a bare
// BaseItemDto array here, not a BaseItemDtoQueryResult envelope (confirmed
// against the OpenAPI schema, not guessed — UserLibraryController's
// GetLatestMedia return type).
//
// A request scoped to a TV-shows library groups its result by series
// (latest_grouping.go, docs/12's "Items/Latest — series grouping") rather
// than returning the flat, ungrouped list below directly — that grouping
// only applies when the caller wants it (grouped, confirmed live against
// the reference server as the groupItems query param, defaulting to
// grouped — see latest_grouping.go's doc comment) and when parentId
// resolves to a library whose CollectionType is tvshows specifically; every
// other scope (a movies library, a mixed/music library, or no parentId at
// all — the home view aggregating every library) keeps this handler's
// original flat behavior, which is already correct for those per docs/12's
// "Movies — confirmed, no grouping" section.
func (s *Server) handleItemsLatest(w http.ResponseWriter, r *http.Request) {
	q := caseInsensitiveQuery(r)
	ctx := r.Context()

	filter := storage.ItemFilter{
		Recursive: true,
		Limit:     defaultLatestLimit,
		SortBy:    []storage.SortField{{By: storage.SortByDateCreated, Descending: true}},
	}
	s.applyParentID(r, &filter)

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	grouped := !strings.EqualFold(q.Get("groupitems"), "false")

	var items []storage.Item
	if grouped && filter.LibraryID != nil {
		if lib, err := s.store.GetLibrary(ctx, *filter.LibraryID); err == nil && lib.CollectionType == storage.CollectionTypeTVShows {
			ac, _ := AuthFromContext(ctx)
			var userID storage.UserID
			if ac != nil {
				userID = ac.User.ID
			}
			grp, err := s.tvShowsLatestGrouped(ctx, *filter.LibraryID, filter.Limit, userID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			items = grp
		}
	}

	if items == nil {
		page, err := s.store.QueryItems(ctx, filter)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		items = page.Items
	}

	dtos := make([]baseItemDto, 0, len(items))
	for _, it := range items {
		dtos = append(dtos, s.baseItemDto(ctx, it))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// mediaTypeItemTypes maps a wire MediaType value (mediaTypeFor's output,
// dto.go) back to the storage.ItemType values that produce it — the
// inverse of that lookup, needed because MediaType is a derived, query-time
// field rather than a stored column, so filtering by it means filtering by
// the ItemTypes behind it. A MediaType with no entry here (e.g. "Book" —
// this project has no ItemType that ever maps to it) intentionally matches
// nothing rather than being silently ignored, see mediaTypesFilter below.
var mediaTypeItemTypes = map[string][]storage.ItemType{
	"Video": {
		storage.ItemTypeMovie, storage.ItemTypeSeries, storage.ItemTypeSeason,
		storage.ItemTypeEpisode, storage.ItemTypeTrailer,
	},
	"Audio": {storage.ItemTypeMusicArtist, storage.ItemTypeMusicAlbum, storage.ItemTypeAudio},
}

// mediaTypesNoMatch is appended to an ItemFilter.ItemTypes result when the
// caller asked for a MediaType this project has no backing ItemType for
// (mediaTypeItemTypes above). storage.ItemFilter.ItemTypes only filters
// when non-empty (sqlite/item.go), so an unmapped MediaType needs an
// explicit type value that can never match a real item.type column instead
// of an empty slice, which would disable the filter and return everything.
const mediaTypesNoMatch storage.ItemType = "__no_media_type_match__"

// mediaTypesFilter translates a request's repeated mediaTypes query param
// (jellyfin-web issues one GET /UserItems/Resume call per home-screen
// "Continue Watching/Listening/Reading" row, each with its own single
// mediaTypes value) into the []storage.ItemType to filter by. Absent
// entirely, it returns nil (no filtering — every ItemType matches).
func mediaTypesFilter(q []string) []storage.ItemType {
	if len(q) == 0 {
		return nil
	}
	var types []storage.ItemType
	for _, mt := range q {
		if ts, ok := mediaTypeItemTypes[strings.TrimSpace(mt)]; ok {
			types = append(types, ts...)
		}
	}
	if types == nil {
		types = []storage.ItemType{mediaTypesNoMatch}
	}
	return types
}

// handleUserItemsResume serves GET /UserItems/Resume — the home screen's
// "Continue Watching" row, and the same query jellyfin-web's item-detail
// resume check ultimately depends on being non-empty. Requires the caller's
// own playstate (storage.ItemFilter.Resume, sqlite/item.go), so unlike
// handleItemsList this always sets UserID from the authenticated caller
// rather than accepting one as a query param — resume state is never
// meaningful for anyone but the requester.
func (s *Server) handleUserItemsResume(w http.ResponseWriter, r *http.Request) {
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	q := r.URL.Query()
	userID := ac.User.ID
	filter := storage.ItemFilter{
		UserID:    &userID,
		Resume:    true,
		Recursive: true,
		Limit:     defaultItemsLimit,
		ItemTypes: mediaTypesFilter(q["mediaTypes"]),
	}
	s.applyParentID(r, &filter)

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, s.baseItemDto(r.Context(), it))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: resolveTotalRecordCount(q.Get("enableTotalRecordCount"), len(items), page.TotalCount),
		StartIndex:       page.Offset,
	})
}

// applyParentID resolves a request's parentId query param the same way
// handleItemsList does (a library root ID vs. an ordinary item ID) — pulled
// out since handleItemsLatest and handleUserItemsResume both need the exact
// same resolution.
func (s *Server) applyParentID(r *http.Request, filter *storage.ItemFilter) {
	v := r.URL.Query().Get("parentId")
	if v == "" {
		return
	}
	if _, err := s.store.GetLibrary(r.Context(), storage.LibraryID(v)); err == nil {
		libID := storage.LibraryID(v)
		filter.LibraryID = &libID
	} else {
		id := storage.ItemID(v)
		filter.ParentID = &id
	}
}

// handleShowSeasons serves GET /Shows/{seriesId}/Seasons — the series detail
// page's season list. Found missing, not just unpolished, via a live
// jellyfin-web session: without it, a series' detail page renders its
// header and nothing else, since the client has no way to discover the
// series' seasons at all. Seasons are a season's direct children of the
// series (item.go's "episode -> season -> series" ParentID chain), so a
// non-recursive ParentID filter is exactly "this series' seasons" — no
// SeriesID filter needed here the way handleShowEpisodes needs one below.
func (s *Server) handleShowSeasons(w http.ResponseWriter, r *http.Request) {
	seriesID := storage.ItemID(r.PathValue("seriesId"))
	filter := storage.ItemFilter{
		ParentID:  &seriesID,
		ItemTypes: []storage.ItemType{storage.ItemTypeSeason},
		SortBy:    []storage.SortField{{By: storage.SortByIndexNumber}},
		Limit:     defaultItemsLimit,
	}
	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, s.baseItemDto(r.Context(), it))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: page.TotalCount,
		StartIndex:       page.Offset,
	})
}

// handleShowEpisodes serves GET /Shows/{seriesId}/Episodes — the season
// detail page's episode list (and, without a seasonId, "every episode of
// this series" for callers that want the whole run flattened). Found
// missing, not just unpolished, the same way as handleShowSeasons above: a
// season's detail page rendered its header with no episode list below it at
// all.
//
// With a seasonId query param, episodes are that season's direct children
// (ParentID, same reasoning as handleShowSeasons); without one, ParentID
// alone can't express "every episode across every season", so this is the
// one caller that needs ItemFilter.SeriesID (types.go) instead.
func (s *Server) handleShowEpisodes(w http.ResponseWriter, r *http.Request) {
	seriesID := storage.ItemID(r.PathValue("seriesId"))
	filter := storage.ItemFilter{
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode},
		SortBy:    []storage.SortField{{By: storage.SortByParentIndexNumber}, {By: storage.SortByIndexNumber}},
		Limit:     defaultItemsLimit,
	}
	if v := r.URL.Query().Get("seasonId"); v != "" {
		seasonID := storage.ItemID(v)
		filter.ParentID = &seasonID
	} else {
		filter.SeriesID = &seriesID
	}
	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, s.baseItemDto(r.Context(), it))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: page.TotalCount,
		StartIndex:       page.Offset,
	})
}

// handleItemAncestors serves GET /Items/{itemId}/Ancestors — the breadcrumb
// chain from an item up to its library root, as a bare BaseItemDto array
// (same envelope-less shape as handleItemsLatest, confirmed against the
// OpenAPI schema's GetAncestors). item_ancestors (storage/sqlite's schema)
// exists but is never populated by anything today, so this walks Item.ParentID
// directly rather than reading that table — cheap given how shallow the
// chains this project's item types ever form actually are (season -> series
// at most), then appends the library root itself (not an Item, so built via
// libraryBaseItemDto rather than one more GetItem hop).
func (s *Server) handleItemAncestors(w http.ResponseWriter, r *http.Request) {
	id := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ancestors := []baseItemDto{}
	for item.ParentID != nil {
		parent, err := s.store.GetItem(r.Context(), *item.ParentID)
		if err != nil {
			break
		}
		ancestors = append(ancestors, s.baseItemDto(r.Context(), parent))
		item = parent
	}
	if lib, err := s.store.GetLibrary(r.Context(), item.LibraryID); err == nil {
		ancestors = append(ancestors, s.libraryBaseItemDto(r.Context(), lib))
	}

	writeJSON(w, http.StatusOK, ancestors)
}

// handleItemSimilar serves GET /Items/{itemId}/Similar. Always empty —
// there's no similarity/recommendation algorithm in this slice (that's
// metadata-provider-adjacent, 08, same boundary as Studios/Filters below) —
// but registered rather than left 404ing: found by testing against a real
// jellyfin-web build that its item-detail page shows the "More Like This"
// section unconditionally before the request resolves (no .catch), so a 404
// leaves a permanently-empty visible section instead of a cleanly-hidden
// one. An honest empty QueryResult lets the client hide it itself.
func (s *Server) handleItemSimilar(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: []baseItemDto{}})
}

// filterOptions mirrors the reference server's QueryFilters/QueryFiltersLegacy
// response shape (GET /Items/Filters2 and the older /Items/Filters), trimmed
// to always-empty: this project has no genre/tag/studio scanning yet (08),
// so an honest empty set of facets is accurate today, not a stand-in for
// real aggregation. Registered instead of left 404ing purely to stop
// FilterButton (jellyfin-web, called unconditionally on every library-view
// mount, not lazily on popover open) from failing every visit — see
// handleItemSimilar's doc comment for the same reasoning.
type filterOptions struct {
	Genres          []string `json:"Genres"`
	Tags            []string `json:"Tags"`
	OfficialRatings []string `json:"OfficialRatings"`
	Years           []int    `json:"Years"`
}

func (s *Server) handleItemsFilters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, filterOptions{Genres: []string{}, Tags: []string{}, OfficialRatings: []string{}, Years: []int{}})
}

func (s *Server) handleItemsFilters2(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, filterOptions{Genres: []string{}, Tags: []string{}, OfficialRatings: []string{}, Years: []int{}})
}

// handleStudios serves GET /Studios. Same "no such metadata yet, honest
// empty over 404" reasoning as handleItemSimilar/handleItemsFilters — there
// is no Studios field on storage.Item at all today (dto.go's baseItemDto
// doc comment).
func (s *Server) handleStudios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: []baseItemDto{}})
}

// handleItemImage serves GET /Items/{itemId}/Images/{imageType} and
// .../Images/{imageType}/{imageIndex} — both routes point at this same
// handler; imageIndex simply reads as empty via r.PathValue on the
// no-index route (see server.go's routes registration).
//
// Unauthenticated, unlike every other route in this slice (see its
// registration in server.go) — confirmed necessary, not just a
// reference-server nicety, by testing against a real jellyfin-web build:
// the <img> tags it renders for posters/backdrops carry neither a token
// header (browsers don't attach custom headers to <img src>) nor an
// api_key/token query param, so requiring auth here left every image
// 401ing silently.
func (s *Server) handleItemImage(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	kind := storage.ImageKind(r.PathValue("imageType"))

	refs, err := s.store.GetImageRefs(r.Context(), itemID, kind)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(refs) == 0 {
		// No scanner exists yet to have populated an image ref (docs/08 is
		// future work) — an expected gap right now, not a bug to mask.
		// 404 cleanly instead of panicking on an empty slice.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	index := 0
	if v := r.PathValue("imageIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			index = n
		}
	}

	ref := refs[0]
	for _, candidate := range refs {
		if candidate.Index == index {
			ref = candidate
			break
		}
	}

	if ref.Path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if _, err := os.Stat(ref.Path); err != nil {
		// The ref exists in storage but the file behind it isn't on disk
		// (e.g. removed out-of-band) — same "expected gap, not a crash"
		// handling as the no-ref case above.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if ref.Tag != "" {
		w.Header().Set("ETag", `"`+ref.Tag+`"`)
	}
	// ref.Path is a trusted, already-resolved filesystem path from
	// storage (see ImageStore's doc comment: refs point at files on disk,
	// not blobs) — the same trust boundary handleVideoStream documents in
	// playback.go. No request-supplied path fragment is ever joined into
	// a filesystem path here.
	http.ServeFile(w, r, ref.Path)
}
