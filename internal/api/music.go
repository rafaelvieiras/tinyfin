package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file holds the music side of the item model (specs/music.md): the
// artist and album members of audio items, the counts of artists and albums,
// the artist listings and the direct audio stream.

// musicArtistItem finds the artist item of a library that has the given name:
// the artist folder when the library has one, else the artist made from the
// name. ok is false when there is none.
func (s *Server) musicArtistItem(ctx context.Context, libraryID storage.LibraryID, name string) (storage.Item, bool) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		LibraryID: &libraryID, Recursive: true, IncludeExtras: true,
		ItemTypes: []storage.ItemType{storage.ItemTypeMusicArtist}, NameContains: name, Limit: 50,
	})
	if err != nil {
		return storage.Item{}, false
	}
	var virtual *storage.Item
	for i, it := range page.Items {
		if !strings.EqualFold(it.Name, name) {
			continue
		}
		if it.ExtraType == "" {
			return it, true
		}
		virtual = &page.Items[i]
	}
	if virtual != nil {
		return *virtual, true
	}
	return storage.Item{}, false
}

// musicNameRefs resolves artist names to {Name, Id} pairs. A name no artist
// item carries yet (a scan in progress) gets the identifier the scan will
// give it.
func (s *Server) musicNameRefs(ctx context.Context, libraryID storage.LibraryID, names []string) []nameIDPairDto {
	out := make([]nameIDPairDto, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		if it, ok := s.musicArtistItem(ctx, libraryID, n); ok {
			out = append(out, nameIDPairDto{Name: it.Name, ID: string(it.ID)})
			continue
		}
		out = append(out, nameIDPairDto{Name: n, ID: string(scan.VirtualArtistID(libraryID, n))})
	}
	return out
}

// decorateMusic completes the DTO of an audio, album or artist item.
func (s *Server) decorateMusic(ctx context.Context, item storage.Item, dto *baseItemDto) {
	if (item.Type == storage.ItemTypeAudio || item.Type == storage.ItemTypeMusicAlbum) && dto.Artists == nil {
		dto.Artists = []string{} // always listed on a track and an album
	}
	switch item.Type {
	case storage.ItemTypeAudio:
		zero := time.Time{}
		if dto.PremiereDate == nil {
			// The reference always reports a premiere date for a track; it is
			// the zero date when the tags have none.
			dto.PremiereDate = &wireTime{zero}
		}
		hasLyrics := false
		if lp, _ := lyricsFileFor(item.Path); lp != "" {
			hasLyrics = true
			// The lyrics file is a stream of the track, after its own.
			add := func(streams []mediaStreamDto) []mediaStreamDto {
				return append(streams, mediaStreamDto{Type: "Lyric", Index: len(streams), Path: lp})
			}
			if len(dto.MediaStreams) > 0 {
				dto.MediaStreams = add(dto.MediaStreams)
			}
			for i := range dto.MediaSources {
				dto.MediaSources[i].MediaStreams = add(dto.MediaSources[i].MediaStreams)
			}
		}
		dto.HasLyrics = &hasLyrics
		dto.ArtistItems = s.musicNameRefs(ctx, item.LibraryID, item.Artists)
		if item.AlbumArtist != "" {
			dto.AlbumArtists = s.musicNameRefs(ctx, item.LibraryID, []string{item.AlbumArtist})
		} else {
			dto.AlbumArtists = []nameIDPairDto{}
		}
		if item.ParentID != nil {
			if album, err := s.store.GetItem(ctx, *item.ParentID); err == nil && album.Type == storage.ItemTypeMusicAlbum {
				dto.AlbumID = string(album.ID)
				if tags, _, _, _ := s.imageTagsFor(ctx, album.ID); tags["Primary"] != "" {
					dto.AlbumPrimaryImageTag = tags["Primary"]
				}
			}
		}
	case storage.ItemTypeMusicAlbum:
		dto.ArtistItems = s.musicNameRefs(ctx, item.LibraryID, item.Artists)
		if item.AlbumArtist != "" {
			dto.AlbumArtists = s.musicNameRefs(ctx, item.LibraryID, []string{item.AlbumArtist})
		} else {
			dto.AlbumArtists = []nameIDPairDto{}
		}
		dto.CumulativeRunTimeTicks = item.RunTimeTicks
		dateLastAdded := wireTime{time.Time{}}
		dto.DateLastMediaAdded = &dateLastAdded
		if n, err := s.childCountOf(ctx, item); err == nil {
			dto.ChildCount = &n
		}
		if n, err := s.recursiveItemCountOf(ctx, item); err == nil {
			dto.RecursiveItemCount = &n
		}
	case storage.ItemTypeMusicArtist:
		dateLastAdded := wireTime{time.Time{}}
		dto.DateLastMediaAdded = &dateLastAdded
		if item.ExtraType == storage.ExtraTypeVirtualArtist {
			dto.Path = ""
		}
		songs, _ := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &item.LibraryID, Recursive: true, ItemTypes: []storage.ItemType{storage.ItemTypeAudio},
			ArtistNames: []string{item.Name}, Limit: 10000,
		})
		albums, _ := s.store.QueryItems(ctx, storage.ItemFilter{
			LibraryID: &item.LibraryID, Recursive: true, ItemTypes: []storage.ItemType{storage.ItemTypeMusicAlbum},
			ArtistNames: []string{item.Name}, Limit: 1,
		})
		zero := 0
		songCount, albumCount := songs.TotalCount, albums.TotalCount
		dto.SongCount, dto.AlbumCount = &songCount, &albumCount
		child := songCount + albumCount
		dto.ChildCount = &child
		dto.ArtistCount, dto.MovieCount, dto.SeriesCount, dto.EpisodeCount = &zero, &zero, &zero, &zero
		dto.TrailerCount, dto.ProgramCount, dto.MusicVideoCount = &zero, &zero, &zero
		if item.ExtraType == "" {
			if n, err := s.recursiveItemCountOf(ctx, item); err == nil {
				dto.RecursiveItemCount = &n
			}
			var ticks int64
			var genres []string
			seen := map[string]bool{}
			if inside, err := s.store.QueryItems(ctx, storage.ItemFilter{
				ParentID: &item.ID, Recursive: true, ItemTypes: []storage.ItemType{storage.ItemTypeAudio},
				SortBy: []storage.SortField{{By: storage.SortByName}}, Limit: 10000,
			}); err == nil {
				for _, t := range inside.Items {
					if t.RunTimeTicks != nil {
						ticks += *t.RunTimeTicks
					}
					for _, g := range t.Genres {
						if k := strings.ToLower(g); !seen[k] {
							seen[k] = true
							genres = append(genres, g)
						}
					}
				}
			}
			dto.CumulativeRunTimeTicks = &ticks
			dto.RunTimeTicks = &ticks
			if len(item.Genres) > 0 {
				// An artist.nfo names the genres; else the tracks' do.
				genres = item.Genres
			}
			dto.Genres = genres
			dto.GenreItems = genreItemsFor(genres)
		} else {
			var ticks int64
			for _, t := range songs.Items {
				if t.RunTimeTicks != nil {
					ticks += *t.RunTimeTicks
				}
			}
			dto.RunTimeTicks = &ticks
		}
	}
}

// audioContentTypes are the content types of the audio files streamed as they
// are, by extension.
var audioContentTypes = map[string]string{
	".mp3": "audio/mpeg", ".flac": "audio/flac", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
	".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav", ".wma": "audio/x-ms-wma",
	".mka": "audio/x-matroska", ".aiff": "audio/aiff", ".aif": "audio/aiff", ".ape": "audio/x-ape",
}

func extOf(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

// artistListingKey carries the role of an /Artists listing through the items
// query: "Artist" for /Artists, "AlbumArtist" for /Artists/AlbumArtists.
type artistListingKey struct{}

// handleArtists serves GET /Artists and GET /Artists/AlbumArtists: the
// artist items (folders and names the tags carry) that appear on a track, or
// on an album, as items of type MusicArtist.
func (s *Server) handleArtists(role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.WithContext(context.WithValue(r.Context(), artistListingKey{}, role))
		u := *r.URL
		q := u.Query()
		for k := range q {
			// The item-type and tree members do not apply to an artist listing.
			switch strings.ToLower(k) {
			case "includeitemtypes", "excludeitemtypes", "recursive", "parentid", "mediatypes":
				q.Del(k)
			}
		}
		q.Set("Recursive", "true")
		q.Set("IncludeItemTypes", "MusicArtist")
		u.RawQuery = q.Encode()
		r2.URL = &u
		s.handleItemsList(w, r2)
	}
}

// applyMusicFilters reads ArtistIds, AlbumArtistIds, AlbumIds, Artists and
// AlbumArtists. It reports false when an identifier names nothing (the
// listing is then empty).
func (s *Server) applyMusicFilters(ctx context.Context, q url.Values, f *storage.ItemFilter) bool {
	ok := true
	names := func(key string) []string {
		var out []string
		for _, tok := range itemsListParam(q, key) {
			it, err := s.store.GetItem(ctx, storage.ItemID(tok))
			if err != nil {
				ok = false
				continue
			}
			out = append(out, it.Name)
		}
		return out
	}
	f.ArtistNames = append(f.ArtistNames, names("artistids")...)
	f.AlbumArtistNames = append(f.AlbumArtistNames, names("albumartistids")...)
	f.ContributingNames = append(f.ContributingNames, names("contributingartistids")...)
	for _, tok := range itemsListParam(q, "albumids") {
		f.AlbumIDs = append(f.AlbumIDs, storage.ItemID(tok))
	}
	f.ArtistNames = append(f.ArtistNames, pipeListParam(q, "artists")...)
	f.AlbumArtistNames = append(f.AlbumArtistNames, pipeListParam(q, "albumartists")...)
	return ok
}
