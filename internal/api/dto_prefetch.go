package api

import (
	"context"
	"sort"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// dtoPrefetch holds, for one page of items, the answers baseItemDto would
// otherwise fetch one item at a time.
//
// It exists because a list route was measured at seven store statements per
// card (docs/15-response-cache-analysis.md): six picture lookups plus Backdrop,
// the caller's playstate, a folder's leaf counts, two counts, and one lookup per
// ancestor. On a grouped "recently added" row that was 112 statements for 16
// cards, and after the grouping path itself was batched it was the largest part
// of the row's 140 ms. Every question here is answered for the whole page in one
// statement, and a card whose question cannot be answered that way (a library
// root folder, which counts its own top-level items) simply falls back to the
// per-item query.
//
// The sets are separate on purpose. "Was this answered?" must mean exactly the
// question that was asked: an item may be prefetched for pictures and
// playstates and not for counts, and a miss must never be read as a zero.
type dtoPrefetch struct {
	imaged  map[storage.ItemID]bool
	played  map[storage.ItemID]bool
	leafed  map[storage.ItemID]bool
	counted map[storage.ItemID]bool

	images     map[storage.ItemID]map[storage.ImageKind][]storage.ImageRef
	playstates map[storage.ItemID]storage.Playstate
	leaves     map[storage.ItemID]storage.LeafCount
	direct     map[storage.ItemID]int
	merged     map[storage.ItemID]int
	recursive  map[storage.ItemID]int
	ancestorOf map[storage.ItemID]storage.Item
	askedFor   map[storage.ItemID]bool
	// tagSets memoises imageTagsFor per item: a card asks for its own picture
	// tags and then asks the same question again for each ancestor it inherits
	// from.
	tagSets map[storage.ItemID]*imageTagSet
}

type dtoPrefetchKey struct{}

// prefetchDTOs answers, in a handful of queries, the per-card questions the
// DTOs for items will ask, and returns a context carrying the answers.
//
// A caller that forgets this helper is only slower, never wrong: every lookup
// falls back to the query it replaced.
func (s *Server) prefetchDTOs(ctx context.Context, items []storage.Item) context.Context {
	if len(items) == 0 {
		return ctx
	}
	pf := &dtoPrefetch{
		imaged:     make(map[storage.ItemID]bool, len(items)),
		played:     make(map[storage.ItemID]bool, len(items)),
		leafed:     map[storage.ItemID]bool{},
		counted:    map[storage.ItemID]bool{},
		ancestorOf: map[storage.ItemID]storage.Item{},
		askedFor:   map[storage.ItemID]bool{},
		tagSets:    map[storage.ItemID]*imageTagSet{},
	}
	ids := make([]storage.ItemID, 0, len(items))
	var countIDs, leafIDs []storage.ItemID
	ancestorIDs := map[storage.ItemID]bool{}
	for _, it := range items {
		if it.ID == "" {
			continue
		}
		ids = append(ids, it.ID)
		pf.played[it.ID] = true
		if isMarkFolder(it.Type) {
			leafIDs = append(leafIDs, it.ID)
			pf.leafed[it.ID] = true
		}
		// A root folder counts what the library holds, not its children: that
		// question needs the library's paths, so it keeps the per-item query.
		if childCountItemTypes[it.Type] {
			countIDs = append(countIDs, it.ID)
			pf.counted[it.ID] = true
		}
		// An ancestor's pictures are part of the card too: a Season or Series
		// card inherits logos, thumbs and backdrops from above, and an Episode
		// card additionally names its series' primary picture. Answering those
		// per card was the largest part of this N+1 — 49 picture lookups for one
		// episode card in a 100-item page.
		for _, ref := range []*storage.ItemID{it.SeriesID, it.SeasonID, it.ParentID} {
			if ref != nil && *ref != "" && !ancestorIDs[*ref] {
				ancestorIDs[*ref] = true
			}
		}
	}
	imageIDs := append(append([]storage.ItemID{}, ids...), keysOf(ancestorIDs)...)
	kinds := append(append([]storage.ImageKind{}, singleInstanceImageKinds...), storage.ImageKind("Backdrop"))
	if m, err := s.store.ImageRefsFor(ctx, imageIDs, kinds); err == nil {
		pf.images = m
		for _, id := range imageIDs {
			pf.imaged[id] = true
		}
	}
	var userID storage.UserID
	if ac, ok := AuthFromContext(ctx); ok && ac.Authenticated {
		userID = ac.User.ID
	}
	if userID != "" {
		if m, err := s.store.PlaystatesFor(ctx, userID, ids); err == nil {
			pf.playstates = m
		} else {
			for id := range pf.played {
				delete(pf.played, id)
			}
		}
		if len(leafIDs) > 0 {
			if m, err := s.store.LeafCountsFor(ctx, userID, leafIDs); err == nil {
				pf.leaves = m
			} else {
				for id := range pf.leafed {
					delete(pf.leafed, id)
				}
			}
		}
	}
	if len(countIDs) > 0 {
		direct, errDirect := s.store.ChildCountsFor(ctx, countIDs)
		merged, errMerged := s.store.OwnedExtraCountsFor(ctx, countIDs, storage.ExtraTypeMergedSeason)
		recursive, errRecursive := s.store.RecursiveItemCountsFor(ctx, countIDs, storage.FolderItemTypes)
		if errDirect == nil && errMerged == nil && errRecursive == nil {
			pf.direct, pf.merged, pf.recursive = direct, merged, recursive
		} else {
			for id := range pf.counted {
				delete(pf.counted, id)
			}
		}
	}
	if len(ancestorIDs) > 0 {
		ancestorList := make([]storage.ItemID, 0, len(ancestorIDs))
		for id := range ancestorIDs {
			ancestorList = append(ancestorList, id)
		}
		for id := range ancestorIDs {
			pf.askedFor[id] = true
		}
		if page, err := s.store.QueryItems(ctx, storage.ItemFilter{IDs: ancestorList, Limit: len(ancestorList)}); err == nil {
			for _, it := range page.Items {
				pf.ancestorOf[it.ID] = it
			}
		} else {
			for id := range pf.askedFor {
				delete(pf.askedFor, id)
			}
		}
	}
	return context.WithValue(ctx, dtoPrefetchKey{}, pf)
}

// dtoPrefetchFrom returns the answers carried by ctx, or nil when the caller did
// not prefetch this page.
func dtoPrefetchFrom(ctx context.Context) *dtoPrefetch {
	pf, _ := ctx.Value(dtoPrefetchKey{}).(*dtoPrefetch)
	return pf
}

// imageRefs is GetImageRefs with the page's answers in front of it.
func (s *Server) imageRefs(ctx context.Context, id storage.ItemID, kind storage.ImageKind) ([]storage.ImageRef, error) {
	if pf := dtoPrefetchFrom(ctx); pf != nil && pf.imaged[id] {
		return pf.images[id][kind], nil
	}
	return s.store.GetImageRefs(ctx, id, kind)
}

// playstate is GetPlaystate with the page's answers in front of it.
func (s *Server) playstate(ctx context.Context, userID storage.UserID, id storage.ItemID) storage.Playstate {
	if pf := dtoPrefetchFrom(ctx); pf != nil && pf.played[id] {
		return pf.playstates[id]
	}
	state, err := s.store.GetPlaystate(ctx, userID, id)
	if err != nil {
		return storage.Playstate{}
	}
	return state
}

// leafCounts is FolderLeafCounts with the page's answers in front of it.
func (s *Server) leafCounts(ctx context.Context, userID storage.UserID, id storage.ItemID) (storage.LeafCount, bool) {
	if pf := dtoPrefetchFrom(ctx); pf != nil && pf.leafed[id] {
		c := pf.leaves[id]
		return c, true
	}
	total, unplayed, err := s.store.FolderLeafCounts(ctx, userID, id)
	if err != nil {
		return storage.LeafCount{}, false
	}
	return storage.LeafCount{Total: total, Unplayed: unplayed}, true
}

// directChildCount and recursiveCount are childCountOf/recursiveItemCountOf with
// the page's answers in front of them. ok is false when this item's counts were
// not prefetched, so the caller runs the original query.
func (s *Server) prefetchedChildCounts(ctx context.Context, item storage.Item) (child, recursive int, ok bool) {
	pf := dtoPrefetchFrom(ctx)
	if pf == nil || !pf.counted[item.ID] {
		return 0, 0, false
	}
	return pf.direct[item.ID] + pf.merged[item.ID], pf.recursive[item.ID], true
}

// prefetchedItem is GetItem with the page's ancestors in front of it. The second
// result is false when the id was not prefetched; an id that was prefetched and
// is absent from the map has no row, which the callers treat as "no ancestor".
func (s *Server) prefetchedItem(ctx context.Context, id storage.ItemID) (storage.Item, bool, bool) {
	pf := dtoPrefetchFrom(ctx)
	if pf == nil || !pf.askedFor[id] {
		return storage.Item{}, false, false
	}
	it, found := pf.ancestorOf[id]
	return it, found, true
}

// ancestorItem answers the per-card ancestor lookups baseItemDto makes (a card's
// series, a card's season) from the page's prefetched items.
func (s *Server) ancestorItem(ctx context.Context, id storage.ItemID) (storage.Item, bool) {
	if it, found, asked := s.prefetchedItem(ctx, id); asked {
		return it, found
	}
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return storage.Item{}, false
	}
	return it, true
}

// seriesFor is the series an item belongs to, from the page's prefetched items.
func (s *Server) seriesFor(ctx context.Context, id storage.ItemID) (storage.Item, bool) {
	return s.ancestorItem(ctx, id)
}

// seasonFor is the season an item belongs to, from the page's prefetched items.
func (s *Server) seasonFor(ctx context.Context, id storage.ItemID) (storage.Item, bool) {
	return s.ancestorItem(ctx, id)
}

// imageTagSet is imageTagsFor's answer for one item, computed once per request:
// a card asks for its own tags and every ancestor asks for the same set again
// when the inheritance fields are filled in.
type imageTagSet struct {
	tags        map[string]string
	aspectRatio *float64
	backdrops   []string
	blurHashes  map[string]map[string]string
}

// keysOf returns the ids of a set in a stable order.
func keysOf(m map[storage.ItemID]bool) []storage.ItemID {
	out := make([]storage.ItemID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
