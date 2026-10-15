// Package playlist is the playlist model: a playlist is an item of type
// Playlist that lives in a hidden library of its own, under one folder item.
// Its ordered entries are links to other items (an item may appear more than
// once), and who may read or edit it is a separate access record
// (storage.PlaylistAccess). Playlists never take part in a library scan.
//
// The metadata a playlist shows (genres, studios, rating, running time) is
// derived from its entries and refreshed whenever they change.
package playlist

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/rating"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// LibraryName is the name of the hidden library that holds playlists.
const LibraryName = "Playlists"

// LibraryID is the identifier of the playlists library.
func LibraryID() storage.LibraryID {
	return storage.LibraryID(guid.Derive("tinyfin-playlists-library"))
}

// FolderID is the identifier of the folder item every playlist sits in. It
// is also the identifier of the Playlists folder shown beside the libraries.
func FolderID() storage.ItemID { return storage.ItemID(guid.Derive("tinyfin-playlists-folder")) }

// ViewID is the identifier of the user's Playlists view (the entry of the
// views listing that opens the playlists).
func ViewID() storage.ItemID { return storage.ItemID(guid.Derive("tinyfin-playlists-view")) }

// Dir is where playlist paths are rooted; nothing is ever written there.
func Dir(dataDir string) string { return filepath.Join(dataDir, "playlists") }

func pathName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return ' '
		}
		return r
	}, name)
}

// EnsureLibrary creates the playlists library and its folder item when
// missing.
func EnsureLibrary(ctx context.Context, store storage.Store, dataDir string) error {
	if _, err := store.GetLibrary(ctx, LibraryID()); err != nil {
		dir := Dir(dataDir)
		opts, _ := json.Marshal(map[string]any{"Enabled": true, "PathInfos": []map[string]string{{"Path": dir}}})
		if err := store.UpsertLibrary(ctx, storage.Library{
			ID:             LibraryID(),
			Name:           LibraryName,
			CollectionType: storage.CollectionTypePlaylists,
			Paths:          []string{dir},
			LibraryOptions: opts,
		}); err != nil {
			return err
		}
	}
	if _, err := store.GetItem(ctx, FolderID()); err != nil {
		return store.UpsertItem(ctx, storage.Item{
			ID:             FolderID(),
			Type:           storage.ItemTypeManualPlaylistsFolder,
			LibraryID:      LibraryID(),
			Name:           LibraryName,
			SortName:       "playlists",
			Path:           Dir(dataDir),
			CollectionType: storage.CollectionTypePlaylists,
			DateCreated:    time.Now().UTC().Truncate(time.Second),
		})
	}
	return nil
}

// Spec is what Create needs.
type Spec struct {
	Name      string
	Owner     storage.UserID
	MediaType string // "Video", "Audio", ...; "" decides from the entries
	Open      bool
	Shares    []storage.PlaylistShare
	Entries   []storage.ItemID
}

// Create makes a playlist from spec and returns its item.
func Create(ctx context.Context, store storage.Store, dataDir string, spec Spec) (storage.Item, error) {
	if err := EnsureLibrary(ctx, store, dataDir); err != nil {
		return storage.Item{}, err
	}
	folder := FolderID()
	item := storage.Item{
		ID:          storage.ItemID(guid.New()),
		Type:        storage.ItemTypePlaylist,
		LibraryID:   LibraryID(),
		ParentID:    &folder,
		Name:        spec.Name,
		SortName:    spec.Name,
		Path:        filepath.Join(Dir(dataDir), pathName(spec.Name)),
		DateCreated: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.UpsertItem(ctx, item); err != nil {
		return item, err
	}
	media := spec.MediaType
	if media == "" || strings.EqualFold(media, "Unknown") {
		media = inferMediaType(ctx, store, spec.Entries)
	}
	if err := store.SetPlaylistAccess(ctx, storage.PlaylistAccess{
		PlaylistID: item.ID, OwnerID: spec.Owner, OpenAccess: spec.Open, MediaType: media, Shares: spec.Shares,
	}); err != nil {
		return item, err
	}
	if err := store.SetPlaylistEntries(ctx, item.ID, spec.Entries); err != nil {
		return item, err
	}
	return Refresh(ctx, store, item.ID)
}

// MediaTypeOf is the media type a playable item type carries.
func MediaTypeOf(t storage.ItemType) string {
	switch t {
	case storage.ItemTypeMovie, storage.ItemTypeEpisode, storage.ItemTypeTrailer, storage.ItemTypeVideo:
		return "Video"
	case storage.ItemTypePhoto:
		return "Photo"
	case storage.ItemTypeAudio, storage.ItemTypeMusicAlbum, storage.ItemTypeMusicArtist:
		return "Audio"
	}
	return ""
}

// inferMediaType is the media type of the first entry that has one; a
// playlist with none is an audio playlist.
func inferMediaType(ctx context.Context, store storage.Store, entries []storage.ItemID) string {
	for _, id := range entries {
		if it, err := store.GetItem(ctx, id); err == nil {
			if m := MediaTypeOf(it.Type); m != "" {
				return m
			}
		}
	}
	return "Audio"
}

// Refresh recomputes what a playlist shows from its entries: the union of
// their genres and studios (first appearance order), the lowest rating and
// the total running time (an entry listed twice counts twice).
func Refresh(ctx context.Context, store storage.Store, id storage.ItemID) (storage.Item, error) {
	pl, err := store.GetItem(ctx, id)
	if err != nil {
		return pl, err
	}
	entries, err := store.PlaylistEntries(ctx, id)
	if err != nil {
		return pl, err
	}
	var genres, studios []string
	seenG, seenS := map[string]bool{}, map[string]bool{}
	var ticks int64
	var ratingText string
	var best rating.Score
	haveRating := false
	cache := map[storage.ItemID]storage.Item{}
	for _, e := range entries {
		it, ok := cache[e]
		if !ok {
			var gerr error
			it, gerr = store.GetItem(ctx, e)
			if gerr != nil {
				continue
			}
			cache[e] = it
		}
		for _, g := range it.Genres {
			if k := strings.ToLower(g); !seenG[k] {
				seenG[k] = true
				genres = append(genres, g)
			}
		}
		for _, s := range it.Studios {
			if k := strings.ToLower(s); !seenS[k] {
				seenS[k] = true
				studios = append(studios, s)
			}
		}
		if it.RunTimeTicks != nil {
			ticks += *it.RunTimeTicks
		}
		if sc, ok := rating.Parse(it.OfficialRating); ok {
			if !haveRating || sc.Value < best.Value || (sc.Value == best.Value && sc.Sub < best.Sub) {
				best, ratingText, haveRating = sc, it.OfficialRating, true
			}
		}
	}
	pl.Genres, pl.Studios, pl.OfficialRating = genres, studios, ratingText
	t := ticks
	pl.RunTimeTicks = &t
	return pl, store.UpsertItem(ctx, pl)
}
