// library.go implements the "library and item navigation" group from
// docs/04 (its highest-priority group): user views, item listing/query,
// item detail, and image bytes. Playback negotiation and the actual
// media-file stream live in playback.go.
//
// Explicitly out of scope for this slice (see the task that produced this
// file, and docs/04's lower-priority groups): facet/filter listing
// endpoints, suggestions/instant-mix, collections/playlists CRUD beyond
// what QueryItems already exposes read-only. Left as a one-line gap here
// rather than silently expanded into.
package api

import (
	"net/http"
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
		items = append(items, baseItemDto{
			Name:           lib.Name,
			ServerID:       s.cfg.ServerID,
			ID:             string(lib.ID),
			IsFolder:       true,
			Type:           string(storage.ItemTypeUserView),
			CollectionType: string(lib.CollectionType),
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

// handleItemsList serves GET /Items: maps the subset of query params this
// slice covers (parentId, recursive, includeItemTypes, searchTerm,
// limit/startIndex, sortBy/sortOrder) to a storage.ItemFilter and returns a
// BaseItemDtoQueryResult. Filters beyond this set (isFavorite, isPlayed,
// genres, years, ...) are a deliberate gap, not a silent guess — QueryItems
// supports Played/Favorite via ItemFilter.UserID, but wiring those up is
// left for a later increment.
func (s *Server) handleItemsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := storage.ItemFilter{
		Recursive:  q.Get("recursive") == "true",
		SearchText: q.Get("searchTerm"),
		Limit:      defaultItemsLimit,
	}

	if v := q.Get("parentId"); v != "" {
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

	if v := q.Get("includeItemTypes"); v != "" {
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

	if v := q.Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}

	filter.SortBy = parseSortFields(q.Get("sortBy"), q.Get("sortOrder"))

	page, err := s.store.QueryItems(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	items := make([]baseItemDto, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, s.baseItemDto(it))
	}

	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: page.TotalCount,
		StartIndex:       page.Offset,
	})
}

// handleItemGet serves GET /Items/{itemId}.
func (s *Server) handleItemGet(w http.ResponseWriter, r *http.Request) {
	id := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.baseItemDto(item))
}

// handleItemImage serves GET /Items/{itemId}/Images/{imageType} and
// .../Images/{imageType}/{imageIndex} — both routes point at this same
// handler; imageIndex simply reads as empty via r.PathValue on the
// no-index route (see server.go's routes registration).
//
// Auth simplification: the reference server allows some image requests
// without a full auth token (so an <img> tag can hit it directly without
// custom headers); this slice requires auth uniformly like every other
// route here, for consistency, and documents that as a deliberate
// simplification rather than silently diverging.
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
