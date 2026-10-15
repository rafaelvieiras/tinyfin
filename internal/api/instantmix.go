package api

import (
	"math/rand"
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// instantmix.go implements the Instant Mix routes (docs/contract/specs/music.md,
// "Instant Mix"):
//
//	GET /Items/{itemId}/InstantMix, /Songs/{itemId}/InstantMix,
//	    /Albums/{itemId}/InstantMix, /Artists/{itemId}/InstantMix,
//	    /Playlists/{itemId}/InstantMix
//	GET /Artists/InstantMix?id=, /MusicGenres/InstantMix?id=
//	GET /MusicGenres/{name}/InstantMix
//
// The answer is a random order of the tracks that share a genre with the
// seed. For a seed made of tracks (a song, an album, an artist, a playlist)
// the pool is the union of the genres of those tracks; a seed whose tracks
// have no genre at all matches every track. TotalRecordCount is the size
// of the pool, Limit cuts the page.

// mixSeedTracks resolves one seed item to the tracks it stands for.
func (s *Server) mixSeedTracks(r *http.Request, sc itemsScope, item storage.Item) []storage.Item {
	ctx := r.Context()
	switch item.Type {
	case storage.ItemTypeAudio:
		return []storage.Item{item}
	case storage.ItemTypePlaylist:
		entries, err := s.store.PlaylistEntries(ctx, item.ID)
		if err != nil {
			return nil
		}
		var out []storage.Item
		for _, e := range entries {
			it, err := s.store.GetItem(ctx, e)
			if err == nil && it.Type == storage.ItemTypeAudio && s.itemReadable(ctx, sc, it) {
				out = append(out, it)
			}
		}
		return out
	case storage.ItemTypeMusicAlbum, storage.ItemTypeMusicArtist:
		f := storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeAudio}, Recursive: true, Limit: 100000}
		if item.Type == storage.ItemTypeMusicAlbum {
			f.AlbumIDs = []storage.ItemID{item.ID}
		} else {
			f.ArtistNames = []string{item.Name}
		}
		sc.apply(&f)
		page, err := s.store.QueryItems(ctx, f)
		if err != nil {
			return nil
		}
		return page.Items
	}
	return nil
}

// mixPool is the set of tracks a mix of the seeds is drawn from.
func (s *Server) mixPool(r *http.Request, sc itemsScope, seedTracks []storage.Item, genreNames []string) ([]storage.Item, error) {
	genres := append([]string{}, genreNames...)
	for _, t := range seedTracks {
		genres = append(genres, t.Genres...)
	}
	if len(seedTracks) == 0 && len(genreNames) == 0 {
		return nil, nil
	}
	f := storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypeAudio}, Recursive: true, Limit: 100000}
	if len(genres) > 0 {
		// no genre at all in the seed: every track
		f.Genres = genres
	}
	sc.apply(&f)
	page, err := s.store.QueryItems(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// writeMix shuffles the pool and answers the page.
func (s *Server) writeMix(w http.ResponseWriter, r *http.Request, pool []storage.Item) {
	iq, ok := discoveryQuery(r)
	if !ok {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	total := len(pool)
	if iq.limitGiven && iq.limit >= 0 && iq.limit < len(pool) {
		pool = pool[:iq.limit]
	}
	out := make([]baseItemDto, 0, len(pool))
	for _, it := range pool {
		out = append(out, shapeListItem(s.baseItemDto(r.Context(), it), iq))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: out, TotalRecordCount: total})
}

// mixFromItems serves a mix seeded by items (ids already resolved).
func (s *Server) mixFromItems(w http.ResponseWriter, r *http.Request, sc itemsScope, items []storage.Item) {
	var tracks []storage.Item
	var genreNames []string
	for _, it := range items {
		tracks = append(tracks, s.mixSeedTracks(r, sc, it)...)
	}
	pool, err := s.mixPool(r, sc, tracks, genreNames)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.writeMix(w, r, pool)
}

// handleInstantMixItem serves the {itemId} forms (a genre id is accepted too).
func (s *Server) handleInstantMixItem(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("itemId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "itemId", raw)
		return
	}
	ctx := r.Context()
	sc, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	item, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil {
		if ref, found, _ := s.store.FindNamed(ctx, storage.ItemID(norm)); found && ref.Kind == storage.NamedGenre {
			s.mixFromGenres(w, r, sc, []string{ref.Name})
			return
		}
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	if !s.itemReadable(ctx, sc, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	s.mixFromItems(w, r, sc, []storage.Item{item})
}

func (s *Server) mixFromGenres(w http.ResponseWriter, r *http.Request, sc itemsScope, names []string) {
	pool, err := s.mixPool(r, sc, nil, names)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.writeMix(w, r, pool)
}

// handleInstantMixGenreName serves GET /MusicGenres/{genreName}/InstantMix:
// the segment is a genre name (an id names no genre, and the mix is empty).
func (s *Server) handleInstantMixGenreName(w http.ResponseWriter, r *http.Request) {
	sc, err := s.callerScope(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.mixFromGenres(w, r, sc, []string{r.PathValue("genreName")})
}

// handleInstantMixByIDs serves GET /Artists/InstantMix and
// /MusicGenres/InstantMix: the seed is the item named by the id query value.
func (s *Server) handleInstantMixByIDs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	q := caseInsensitiveQuery(r)
	var items []storage.Item
	var names []string
	for _, tok := range itemsListParam(q, "id") {
		norm, valid := normalizeGUID(tok)
		if !valid {
			writeInvalidID(w, "id", tok)
			return
		}
		if it, err := s.store.GetItem(ctx, storage.ItemID(norm)); err == nil {
			if s.itemReadable(ctx, sc, it) {
				items = append(items, it)
			}
			continue
		}
		if ref, found, _ := s.store.FindNamed(ctx, storage.ItemID(norm)); found && ref.Kind == storage.NamedGenre {
			names = append(names, ref.Name)
			continue
		}
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	var tracks []storage.Item
	for _, it := range items {
		tracks = append(tracks, s.mixSeedTracks(r, sc, it)...)
	}
	pool, err := s.mixPool(r, sc, tracks, names)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.writeMix(w, r, pool)
}
