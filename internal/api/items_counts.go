package api

import (
	"context"
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// itemCounts mirrors GET /Items/Counts's response shape, confirmed live
// against the reference server (docs/04's sanctioned black-box
// verification) with no query string at all: jellyfin-web's Dashboard
// overview page calls this exact form. Field order/names match the
// confirmed response byte-for-byte:
// {"MovieCount":21,"SeriesCount":4,"EpisodeCount":319,"ArtistCount":0,
// "ProgramCount":0,"TrailerCount":0,"SongCount":0,"AlbumCount":0,
// "MusicVideoCount":0,"BoxSetCount":0,"BookCount":0,"ItemCount":0}.
//
// Only MovieCount/SeriesCount/EpisodeCount are ever real numbers here.
// internal/scan/scan.go — the only thing that ever creates an Item today —
// only ever produces storage.ItemTypeMovie/ItemTypeSeries/ItemTypeSeason/
// ItemTypeEpisode (its CollectionType switch only walks Movies/TVShows,
// same gap library.go's package doc comment and localization.go's
// libraryTypeOptionsByType already document for music/book/program/boxset).
// So ArtistCount/ProgramCount/TrailerCount/SongCount/AlbumCount/
// MusicVideoCount/BoxSetCount/BookCount/ItemCount are structural zeros, not
// placeholders awaiting a query: there is no music/book/program/boxset
// scanning pipeline behind any of them to count, the same "minimal over
// guessed" boundary this package already draws in library.go and
// localization.go. ItemCount specifically mirrors the reference server's
// own field (confirmed live: it stays 0 even against a populated library,
// evidently reserved for a BaseItemKind this project has no equivalent
// scan path for either) rather than being repurposed as a grand total.
type itemCounts struct {
	MovieCount      int `json:"MovieCount"`
	SeriesCount     int `json:"SeriesCount"`
	EpisodeCount    int `json:"EpisodeCount"`
	ArtistCount     int `json:"ArtistCount"`
	ProgramCount    int `json:"ProgramCount"`
	TrailerCount    int `json:"TrailerCount"`
	SongCount       int `json:"SongCount"`
	AlbumCount      int `json:"AlbumCount"`
	MusicVideoCount int `json:"MusicVideoCount"`
	BoxSetCount     int `json:"BoxSetCount"`
	BookCount       int `json:"BookCount"`
	ItemCount       int `json:"ItemCount"`
}

// countItemsByType asks QueryItems for exactly one item of the given type
// and reads TotalRecordCount off the page rather than paging through (or
// materializing) every matching row — ItemPage.TotalCount is the
// pre-pagination match count regardless of how small Limit is
// (storage/types.go's doc comment), so Limit: 1 is the cheapest possible
// query that still yields an accurate count. This is the same
// no-full-catalog-materialization discipline AGENTS.md principle 1
// requires of every read in this project, applied to a handler whose only
// job is a number.
func countItemsByType(ctx context.Context, store storage.ItemStore, sc itemsScope, t storage.ItemType) (int, error) {
	f := storage.ItemFilter{
		ItemTypes: []storage.ItemType{t},
		Limit:     1,
	}
	sc.apply(&f)
	page, err := store.QueryItems(ctx, f)
	if err != nil {
		return 0, err
	}
	return page.TotalCount, nil
}

// handleItemsCounts serves GET /Items/Counts — called by jellyfin-web's
// Dashboard overview page with no query parameters at all (confirmed live,
// see itemCounts' doc comment). The reference server's real form of this
// endpoint accepts IsFavorite/UserId query params to scope the counts; this
// project only ever sees (and only needs to answer) the parameterless form
// that call site actually sends, so those aren't read here — a gap to
// revisit only if a real call site is found needing them, not guessed at
// up front.
func (s *Server) handleItemsCounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	movies, err := countItemsByType(ctx, s.store, scope, storage.ItemTypeMovie)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	series, err := countItemsByType(ctx, s.store, scope, storage.ItemTypeSeries)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	episodes, err := countItemsByType(ctx, s.store, scope, storage.ItemTypeEpisode)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, itemCounts{
		MovieCount:   movies,
		SeriesCount:  series,
		EpisodeCount: episodes,
		// Every other field is a structural zero — see itemCounts' doc
		// comment.
	})
}
