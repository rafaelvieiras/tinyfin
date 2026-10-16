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
	"context"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// defaultItemsLimit bounds the listings the reference does cap (seasons,
// next up). storage.ItemFilter.Limit is required and rejected if <= 0
// (docs/05, item.go) so no caller materializes a catalog by accident; the
// two listings the reference leaves uncapped pass unboundedLimit on
// purpose (below).
const defaultItemsLimit = 100

// unboundedLimit is the page size of a listing the reference does not cap
// (GET /Items and GET /UserItems/Resume without Limit return every match,
// verified against it, items.list.R20). Paging, when asked for, still wins.
const unboundedLimit = 1<<31 - 1

// unboundedEpisodesLimit stands in for "no limit" on GET
// /Shows/{seriesId}/Episodes when the client's request carries neither
// Limit nor StartIndex — docs/12's "Shows/Episodes — no default page cap"
// confirms the reference server applies no default page size at all in
// that case (unlike GET /Items above), returning every matching episode.
// storage.ItemFilter.Limit is required and rejected if <= 0 (docs/05, O3),
// so "no limit" can't be expressed as a literal absence of one — this is a
// value high enough to never be the real constraint for any season/series
// a single library realistically holds.
const unboundedEpisodesLimit = 1 << 20

// handleUserViews serves GET /UserViews: the library roots, one BaseItemDto
// per storage.Library. QueryLibraries is intentionally unbounded (docs/05:
// library count is operator-scale, not catalog-scale, same reasoning as
// UserStore.QueryUsers), so no paging is applied here.
func (s *Server) handleUserViews(w http.ResponseWriter, r *http.Request) {
	// Only the libraries the user may access (policy EnableAllFolders /
	// EnabledFolders) are listed.
	ac, _ := AuthFromContext(r.Context())
	scope, err := s.itemsScopeFor(r.Context(), ac.User)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	libs := s.viewLibraries(r.Context(), scope)
	items := make([]baseItemDto, 0, len(libs)+1)
	for _, lib := range libs {
		items = append(items, s.libraryBaseItemDto(r.Context(), lib))
	}
	// The Playlists view is listed once the user can read a playlist; the
	// views are ordered by name.
	if s.userHasPlaylists(r.Context(), scope) {
		items = append(items, s.playlistsViewDto(r.Context()))
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
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
// server (reference server), not assumed: favoriting 8 movies and requesting
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
		// A library's own root folder item is not "recently added media".
		ExcludeItemTypes: []storage.ItemType{storage.ItemTypeFolder, storage.ItemTypeBoxSet},
	}
	// IncludeItemTypes names the kinds asked for (collections are left out
	// of the default "recently added" list).
	var wanted []storage.ItemType
	for _, tok := range itemsListParam(q, "includeitemtypes") {
		if k, ok := itemsKnownKinds[strings.ToLower(tok)]; ok {
			wanted = append(wanted, k)
		}
	}
	// Music (specs/music.md music.latest.R1): grouped, the tracks of an album
	// are listed as that album (an Audio request, or none, lists albums);
	// ungrouped, the albums are left out of the default list. Artists are
	// only listed when asked for.
	musicGrouped := !strings.EqualFold(q.Get("groupitems"), "false")
	// Scoped to a music library and grouped, the types asked for are ignored:
	// the albums are listed (music.latest.R3).
	if musicGrouped {
		if raw := strings.TrimSpace(q.Get("parentid")); raw != "" {
			if g, valid := normalizeGUID(raw); valid {
				if lib, err := s.store.GetLibrary(ctx, storage.LibraryID(g)); err == nil && lib.CollectionType == storage.CollectionTypeMusic {
					wanted = []storage.ItemType{storage.ItemTypeMusicAlbum}
				}
			}
		}
	}
	if len(wanted) > 0 {
		filter.ItemTypes, filter.ExcludeItemTypes = wanted, nil
		if musicGrouped {
			hasAlbum := false
			for _, t := range wanted {
				hasAlbum = hasAlbum || t == storage.ItemTypeMusicAlbum
			}
			out := wanted[:0:0]
			for _, t := range wanted {
				switch {
				case t == storage.ItemTypeAudio && hasAlbum:
				case t == storage.ItemTypeAudio:
					out = append(out, storage.ItemTypeMusicAlbum)
				default:
					out = append(out, t)
				}
			}
			filter.ItemTypes = out
		}
	} else {
		filter.ExcludeItemTypes = append(filter.ExcludeItemTypes, storage.ItemTypeMusicArtist)
		if musicGrouped {
			filter.ExcludeItemTypes = append(filter.ExcludeItemTypes, storage.ItemTypeAudio)
		} else {
			filter.ExcludeItemTypes = append(filter.ExcludeItemTypes, storage.ItemTypeMusicAlbum)
		}
	}
	s.applyParentID(r, &filter)
	scope, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	scope.apply(&filter)
	if filter.LibraryID != nil && !scope.libIDs[*filter.LibraryID] {
		writeJSON(w, http.StatusOK, []baseItemDto{})
		return
	}

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
			// The grouped cards bypass QueryItems, so apply the rules here.
			vis := []storage.Item{}
			for _, g := range grp {
				if s.itemReadable(ctx, scope, g) {
					vis = append(vis, g)
				}
			}
			items = vis
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
		storage.ItemTypeEpisode, storage.ItemTypeTrailer, storage.ItemTypeVideo,
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

// applyParentID resolves a request's parentId query param the same way
// handleItemsList does (a library root ID vs. an ordinary item ID) — pulled
// out since handleItemsLatest and handleUserItemsResume both need the exact
// same resolution.
func (s *Server) applyParentID(r *http.Request, filter *storage.ItemFilter) {
	v := caseInsensitiveQuery(r).Get("parentid")
	if v == "" {
		return
	}
	if g, ok := normalizeGUID(v); ok {
		v = g
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
	if !s.seriesReadable(w, r, seriesID, &filter) {
		return
	}
	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// A season without a number (the one that holds files with no season
	// number) comes after the numbered ones.
	sort.SliceStable(page.Items, func(i, j int) bool {
		a, b := page.Items[i].IndexNumber, page.Items[j].IndexNumber
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return *a < *b
	})
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

// orderEpisodes orders a series' episodes the way the series page lists
// them: by season number (an episode with no season number of its own takes
// its season's; a season without a number comes last), then by sort name,
// which already carries the episode number.
func (s *Server) orderEpisodes(ctx context.Context, eps []storage.Item) {
	seasonIdx := map[storage.ItemID]*int{}
	idxOf := func(it storage.Item) *int {
		if it.ParentIndexNumber != nil {
			return it.ParentIndexNumber
		}
		if it.SeasonID == nil {
			return nil
		}
		if v, ok := seasonIdx[*it.SeasonID]; ok {
			return v
		}
		var v *int
		if season, err := s.store.GetItem(ctx, *it.SeasonID); err == nil {
			v = season.IndexNumber
		}
		seasonIdx[*it.SeasonID] = v
		return v
	}
	keys := make(map[storage.ItemID]*int, len(eps))
	for _, it := range eps {
		keys[it.ID] = idxOf(it)
	}
	sort.SliceStable(eps, func(i, j int) bool {
		a, b := keys[eps[i].ID], keys[eps[j].ID]
		switch {
		case a == nil && b == nil:
		case a == nil:
			return false
		case b == nil:
			return true
		case *a != *b:
			return *a < *b
		}
		return eps[i].SortName < eps[j].SortName
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
//
// Unlike handleItemsList, an omitted Limit/StartIndex here does NOT fall
// back to defaultItemsLimit — docs/12's "Shows/Episodes — no default page
// cap" confirms the reference server applies no default page size on this
// endpoint at all, returning every matching episode when the client didn't
// ask for paging. A season with more than defaultItemsLimit episodes (a
// long-running non-Western series, e.g. the 153-episode single-season case
// that surfaced this) was silently truncated before this was fixed.
func (s *Server) handleShowEpisodes(w http.ResponseWriter, r *http.Request) {
	seriesID := storage.ItemID(r.PathValue("seriesId"))
	filter := storage.ItemFilter{
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode},
		SortBy:    []storage.SortField{{By: storage.SortByParentIndexNumber}, {By: storage.SortByIndexNumber}},
		Limit:     unboundedEpisodesLimit,
	}
	// caseInsensitiveQuery: jellyfin-web's season-detail-page call sends
	// "SeasonId" (PascalCase) — confirmed live via Playwright against a real
	// jellyfin-web build. Reading the raw, case-sensitive r.URL.Query() here
	// meant the filter never matched, silently falling through to "every
	// episode of the series" (the no-seasonId branch) instead of just that
	// season's ~12 episodes, breaking the "More from Season X" carousel.
	q := caseInsensitiveQuery(r)
	if v := q.Get("seasonid"); v != "" {
		seasonID := storage.ItemID(v)
		filter.ParentID = &seasonID
		// The episodes below the season (also those inside its folders) and
		// those of the series that carry its number.
		filter.Recursive = true
		filter.ItemTypes = []storage.ItemType{storage.ItemTypeEpisode}
		if season, err := s.store.GetItem(r.Context(), seasonID); err == nil &&
			season.Type == storage.ItemTypeSeason && season.IndexNumber != nil && season.SeriesID != nil {
			filter.SeasonMembers = &storage.SeasonMembers{SeriesID: *season.SeriesID, Index: *season.IndexNumber}
		}
	} else {
		filter.SeriesID = &seriesID
	}
	if !s.seriesReadable(w, r, seriesID, &filter) {
		return
	}

	// Same explicit-limit/startIndex handling as handleItemsList — a client
	// that does send these (e.g. a future paged episode view) is honored
	// exactly like every other listing endpoint; only the no-params case
	// differs from handleItemsList, per this function's doc comment above.
	// The order is decided here (orderEpisodes), so the store returns the
	// whole set and the page is cut afterwards.
	limit, offset := 0, 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := q.Get("startindex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	all := page.Items
	s.orderEpisodes(r.Context(), all)
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	items := make([]baseItemDto, 0, len(all))
	for _, it := range all {
		items = append(items, s.baseItemDto(r.Context(), it))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: page.TotalCount,
		StartIndex:       offset,
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

	if !s.requireReadable(w, r, item) {
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

// liveTvProgramsResult mirrors the reference server's QueryResult<BaseItemDto>
// shape for GET /LiveTv/Programs — same envelope as baseItemDtoQueryResult
// (dto.go), kept as its own alias only so this handler's intent (a LiveTV
// program listing, always empty) reads clearly at the call site.
//
// handleLiveTvPrograms serves GET /LiveTv/Programs. Confirmed live against
// the reference server with no LiveTV tuner configured:
// {"Items":[],"TotalRecordCount":0,"StartIndex":0} (200), not a 404 — same
// "honest empty over 404" precedent as handleItemSimilar/handleStudios
// above (this file's package doc comment): jellyfin-web's series detail
// page calls this unconditionally, so 404ing it broke that page's network
// tab even though nothing in this project implements LiveTV. No LiveTV
// logic exists or is added here — this is a stub, not a partial feature.
func (s *Server) handleLiveTvPrograms(w http.ResponseWriter, r *http.Request) {
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
// ApiKey/token query param, so requiring auth here left every image
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
		n, err := strconv.Atoi(v)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		index = n
	}

	// An index that names no image is a 404, whatever else the item has
	// (images.api.R2).
	var ref storage.ImageRef
	found := false
	for _, candidate := range refs {
		if candidate.Index == index {
			ref, found = candidate, true
			break
		}
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
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
	if r.URL.Query().Get("tag") != "" {
		// A request that names a tag can be cached for good: the tag
		// changes when the picture does.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	// ref.Path is a trusted, already-resolved filesystem path from
	// storage (see ImageStore's doc comment: refs point at files on disk,
	// not blobs) — the same trust boundary handleVideoStream documents in
	// playback.go. No request-supplied path fragment is ever joined into
	// a filesystem path here.
	http.ServeFile(w, r, ref.Path)
}

// itemReadableByCaller is itemReadable for the authenticated caller.
func (s *Server) itemReadableByCaller(r *http.Request, item storage.Item) bool {
	sc, err := s.callerScope(r.Context())
	return err == nil && s.itemReadable(r.Context(), sc, item)
}

// seriesReadable resolves seriesID for a season/episode listing: an unknown
// series or one the caller may not read answers 404, otherwise the caller's
// scope is applied to filter.
func (s *Server) seriesReadable(w http.ResponseWriter, r *http.Request, seriesID storage.ItemID, filter *storage.ItemFilter) bool {
	sc, err := s.callerScope(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	series, err := s.store.GetItem(r.Context(), seriesID)
	if err != nil || !s.itemReadable(r.Context(), sc, series) {
		w.WriteHeader(http.StatusNotFound)
		return false
	}
	sc.apply(filter)
	return true
}
