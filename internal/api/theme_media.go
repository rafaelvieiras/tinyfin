// theme_media.go implements GET /Items/{itemId}/ThemeMedia — the
// background theme-song/theme-video widget a real jellyfin-web detail page
// requests unconditionally on every load (docs/11's route coverage table
// found this 404ing, "called on every single detail page load"; docs/12's
// "Movie detail page — Theme Media" section has the full wire-shape and
// filename-convention writeup this file implements).
//
// Unlike Primary/Backdrop/Logo/Banner (scan.go's seedFolderImages), theme
// media isn't indexed at scan time — it's resolved by scanning the item's
// own folder at request time. Both are defensible per docs/08's scanner
// boundary; request-time was chosen here because theme media has no
// per-item aspect-ratio/dimension metadata worth precomputing (unlike an
// image) and the item's folder is already cheaply derivable from
// storage.Item.Path (filepath.Dir), so there's no real cost to defer this
// past scan time — and it keeps a bug in this code from being able to
// corrupt anything durable in storage.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// themeAudioExtensions is the set of file extensions recognized as a theme
// song, both for the standalone "theme.<ext>" file and for every file
// inside a "theme-music/" subfolder (docs/12's "Movie detail page — Theme
// Media" — sourced from jellyfin.org's published movie-library naming
// guide, not the reference server's C# source). This project has no
// existing audio-extension list to reuse (internal/scan's own
// videoExtensions covers playable video containers only, docs/08's
// video-first v1 scope never having needed one for audio) — the common
// container formats a theme-music file is realistically encoded in.
var themeAudioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".wav": true, ".aac": true, ".opus": true,
}

// themeMediaResult mirrors one of the three parallel result blocks in the
// reference server's GetThemeMedia response (source: its own published
// OpenAPI schema, fetched live — docs/12's "Wire shape" subsection) —
// {Items, TotalRecordCount, StartIndex, OwnerId}, reused identically for
// ThemeVideosResult, ThemeSongsResult, and SoundtrackSongsResult below.
type themeMediaResult struct {
	Items            []baseItemDto `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
	StartIndex       int           `json:"StartIndex"`
	OwnerID          string        `json:"OwnerId"`
}

// themeMediaResponse is GetThemeMedia's top-level envelope.
// SoundtrackSongsResult is always empty in this implementation — not a
// concept movies use (docs/12's "Wire shape" subsection: "no
// SoundtrackSongsResult concept for movies in scope here").
type themeMediaResponse struct {
	ThemeVideosResult     themeMediaResult `json:"ThemeVideosResult"`
	ThemeSongsResult      themeMediaResult `json:"ThemeSongsResult"`
	SoundtrackSongsResult themeMediaResult `json:"SoundtrackSongsResult"`
}

// handleThemeMedia serves GET /Items/{itemId}/ThemeMedia. userId is
// accepted and, if it resolves to the authenticated caller, used to attach
// UserData to each returned entry the same way every other item response
// does (dto.go's userDataFor) — same "accepted but not required to differ
// from ctx's own auth" precedent as handleItemGet's legacy per-user form
// (server.go's comment on that route). inheritFromParent/sortBy/sortOrder:
// only inheritFromParent is read, and only to walk exactly one level up
// Item.ParentID when the item's own folder has nothing — docs/12's scope
// note explains why a deeper/more general implementation isn't warranted
// here (a movie itself has no meaningful ancestor for this purpose; the
// one-level walk exists so an Episode/Season caller sending
// inheritFromParent=true gets a reasonable answer instead of always empty,
// without this handler needing to know TV-specific inheritance rules).
func (s *Server) handleThemeMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	songs, videos := s.localThemeMedia(item)
	if len(songs) == 0 && len(videos) == 0 && strings.EqualFold(r.URL.Query().Get("inheritFromParent"), "true") && item.ParentID != nil {
		if parent, err := s.store.GetItem(ctx, *item.ParentID); err == nil {
			songs, videos = s.localThemeMedia(parent)
		}
	}

	var userID storage.UserID
	if ac, ok := AuthFromContext(ctx); ok && ac.Authenticated {
		userID = ac.User.ID
	}
	for i := range songs {
		songs[i].UserData = s.userDataFor(ctx, userID, storage.Item{ID: storage.ItemID(songs[i].ID), Type: storage.ItemType(songs[i].Type)})
	}
	for i := range videos {
		videos[i].UserData = s.userDataFor(ctx, userID, storage.Item{ID: storage.ItemID(videos[i].ID), Type: storage.ItemType(videos[i].Type)})
	}

	writeJSON(w, http.StatusOK, themeMediaResponse{
		ThemeVideosResult: themeMediaResult{
			Items: videos, TotalRecordCount: len(videos), OwnerID: string(item.ID),
		},
		ThemeSongsResult: themeMediaResult{
			Items: songs, TotalRecordCount: len(songs), OwnerID: string(item.ID),
		},
		SoundtrackSongsResult: themeMediaResult{
			Items: []baseItemDto{}, OwnerID: string(item.ID),
		},
	})
}

// localThemeMedia resolves item's own folder (filepath.Dir(item.Path) —
// the same folder seedFolderImages/scan.go looks in for Primary/Backdrop/
// Logo/Banner artwork) for theme songs and theme videos, per docs/12's
// documented filename convention: a standalone "theme.<ext>" file or any
// audio file inside a "theme-music/" subfolder for songs; any video file
// inside a "backdrops/" subfolder for videos. Both slices are always
// non-nil so the JSON response carries [] rather than null when nothing is
// found (writeJSON has no other normalization step for this).
func (s *Server) localThemeMedia(item storage.Item) (songs, videos []baseItemDto) {
	songs = []baseItemDto{}
	videos = []baseItemDto{}
	if item.Path == "" {
		return songs, videos
	}
	// A Series/Season's Path is already its folder; a file item's own
	// folder is its parent directory.
	folder := filepath.Dir(item.Path)
	if !item.HasMediaFile() {
		folder = item.Path
	}

	entries, err := os.ReadDir(folder)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), "theme") && themeAudioExtensions[strings.ToLower(filepath.Ext(name))] {
				songs = append(songs, s.localMediaItemDto(filepath.Join(folder, name), "Audio"))
			}
		}
	}

	if themeMusicEntries, err := os.ReadDir(filepath.Join(folder, "theme-music")); err == nil {
		for _, e := range themeMusicEntries {
			if e.IsDir() {
				continue
			}
			if themeAudioExtensions[strings.ToLower(filepath.Ext(e.Name()))] {
				songs = append(songs, s.localMediaItemDto(filepath.Join(folder, "theme-music", e.Name()), "Audio"))
			}
		}
	}

	if backdropEntries, err := os.ReadDir(filepath.Join(folder, "backdrops")); err == nil {
		for _, e := range backdropEntries {
			if e.IsDir() {
				continue
			}
			if scan.IsVideoFile(e.Name()) {
				videos = append(videos, s.localMediaItemDto(filepath.Join(folder, "backdrops", e.Name()), "Video"))
			}
		}
	}

	return songs, videos
}

// localMediaItemDto builds a minimal baseItemDto for a local theme
// audio/video file that was found on disk but was never catalogued as a
// storage.Item (docs/12's scope note: "each Items entry is a normal
// BaseItemDto for the underlying local audio/video file, not a synthetic
// wrapper" — but the file itself doesn't need a formally scanned Item row
// behind it, per this task's own instruction to use judgment rather than
// build a second cataloging pass just for theme media). Type/MediaType use
// the reference server's own coarse kinds directly ("Audio"/"Video" —
// mediaTypeFor's doc comment on baseItemDto covers the MediaType
// convention this reuses).
func (s *Server) localMediaItemDto(path, mediaType string) baseItemDto {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return baseItemDto{
		Name:      name,
		ServerID:  s.cfg.ServerID,
		ID:        themeMediaItemID(path),
		Path:      path,
		Container: strings.TrimPrefix(strings.ToLower(filepath.Ext(base)), "."),
		Type:      mediaType,
		MediaType: mediaType,
		IsFolder:  false,
	}
}

// themeMediaItemID mints a stable, canonical-UUID-formatted ID for a local
// theme media file from its filesystem path — deterministic (same
// technique as internal/scan/id.go's deterministicID, duplicated rather
// than imported per that file's own doc comment on why each package that
// needs this keeps its own small copy) so repeated GET /ThemeMedia calls
// for the same file return the same ID instead of a fresh one per request,
// which would otherwise make a client's own per-item caching/UserData
// tracking for theme media meaningless.
func themeMediaItemID(path string) string {
	sum := sha256.Sum256([]byte("theme-media-" + path))
	return formatUUID(hex.EncodeToString(sum[:16]))
}
