package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestThemeMediaFindsLocalFiles regression-guards docs/12's "Movie detail
// page — Theme Media": GET /Items/{itemId}/ThemeMedia used to 404 outright
// (no route registered at all — docs/11's route coverage table), called
// unconditionally on every real jellyfin-web detail page load. This proves
// the populated shape: a standalone theme.mp3, an extra song inside
// theme-music/, and a video inside backdrops/ are all found and reported
// in the right result block.
func TestThemeMediaFindsLocalFiles(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "ivy", "ivypass")
	token := loginToken(t, ts.URL, user.Name, "ivypass")
	get := authedClient(t, ts.URL, token)

	movieDir := t.TempDir()
	videoPath := filepath.Join(movieDir, "movie.mkv")
	if err := os.WriteFile(videoPath, []byte("not a real video"), 0o644); err != nil {
		t.Fatalf("WriteFile(movie.mkv): %v", err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "theme.mp3"), []byte("theme song bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(theme.mp3): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(movieDir, "theme-music"), 0o755); err != nil {
		t.Fatalf("MkdirAll(theme-music): %v", err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "theme-music", "extra.flac"), []byte("extra song bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(theme-music/extra.flac): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(movieDir, "backdrops"), 0o755); err != nil {
		t.Fatalf("MkdirAll(backdrops): %v", err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "backdrops", "clip.mp4"), []byte("theme video bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(backdrops/clip.mp4): %v", err)
	}

	libID := createTestLibrary(t, st, "ThemeMediaLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Themed Movie", Path: videoPath,
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Themed Movie")

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/ThemeMedia")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/{itemId}/ThemeMedia status = %d, want 200", resp.StatusCode)
	}
	var result themeMediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding ThemeMedia response: %v", err)
	}

	if got := result.ThemeSongsResult.TotalRecordCount; got != 2 {
		t.Errorf("ThemeSongsResult.TotalRecordCount = %d, want 2 (theme.mp3 + theme-music/extra.flac)", got)
	}
	songNames := map[string]bool{}
	for _, dto := range result.ThemeSongsResult.Items {
		songNames[dto.Name] = true
		if dto.MediaType != "Audio" {
			t.Errorf("theme song %q MediaType = %q, want Audio", dto.Name, dto.MediaType)
		}
	}
	if !songNames["theme"] {
		t.Error("ThemeSongsResult.Items missing theme.mp3 (as Name \"theme\")")
	}
	if !songNames["extra"] {
		t.Error("ThemeSongsResult.Items missing theme-music/extra.flac (as Name \"extra\")")
	}
	if result.ThemeSongsResult.OwnerID != string(itemID) {
		t.Errorf("ThemeSongsResult.OwnerId = %q, want %q", result.ThemeSongsResult.OwnerID, itemID)
	}

	if got := result.ThemeVideosResult.TotalRecordCount; got != 1 {
		t.Fatalf("ThemeVideosResult.TotalRecordCount = %d, want 1 (backdrops/clip.mp4)", got)
	}
	if got := result.ThemeVideosResult.Items[0].Name; got != "clip" {
		t.Errorf("theme video Name = %q, want clip", got)
	}
	if got := result.ThemeVideosResult.Items[0].MediaType; got != "Video" {
		t.Errorf("theme video MediaType = %q, want Video", got)
	}

	if len(result.SoundtrackSongsResult.Items) != 0 {
		t.Errorf("SoundtrackSongsResult.Items = %v, want empty (movies don't use this block)", result.SoundtrackSongsResult.Items)
	}
}

// TestThemeMediaEmptyWhenNoFilesPresent proves the endpoint returns 200
// with all three result blocks present but empty, rather than 404, for an
// item whose folder has no theme.<ext>/theme-music/backdrops at all —
// docs/12's "Scope note": this is the shape actually confirmed live
// against the reference server (a movie folder with no theme files).
func TestThemeMediaEmptyWhenNoFilesPresent(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "jack", "jackpass")
	token := loginToken(t, ts.URL, user.Name, "jackpass")
	get := authedClient(t, ts.URL, token)

	movieDir := t.TempDir()
	videoPath := filepath.Join(movieDir, "movie.mkv")
	if err := os.WriteFile(videoPath, []byte("not a real video"), 0o644); err != nil {
		t.Fatalf("WriteFile(movie.mkv): %v", err)
	}

	libID := createTestLibrary(t, st, "ThemeMediaEmptyLib", storage.CollectionTypeMovies)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "No Theme Movie", Path: videoPath,
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "No Theme Movie")

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/ThemeMedia")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /Items/{itemId}/ThemeMedia status = %d, want 200", resp.StatusCode)
	}
	var result themeMediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding ThemeMedia response: %v", err)
	}
	if len(result.ThemeSongsResult.Items) != 0 || len(result.ThemeVideosResult.Items) != 0 || len(result.SoundtrackSongsResult.Items) != 0 {
		t.Errorf("all result blocks should be empty, got %+v", result)
	}
}

// TestThemeMediaUnknownItem404s proves a request for an itemId that
// doesn't resolve to any item 404s rather than panicking or returning an
// empty-but-200 response for a nonexistent item.
func TestThemeMediaUnknownItem404s(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "kim", "kimpass")
	token := loginToken(t, ts.URL, user.Name, "kimpass")
	get := authedClient(t, ts.URL, token)

	resp := get(http.MethodGet, "/Items/does-not-exist/ThemeMedia")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /Items/{itemId}/ThemeMedia (unknown item) status = %d, want 404", resp.StatusCode)
	}
}

// TestFolderShapedItemWithPathHasNoFileBehavior guards the scanner storing a
// Series' own folder as Path (the reference server reports the series
// directory there): the folder must not be treated as a playable file
// (no MediaSources, no stream), and theme media is looked up inside the
// folder itself rather than its parent.
func TestFolderShapedItemWithPathHasNoFileBehavior(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "kim", "kimpass")
	token := loginToken(t, ts.URL, user.Name, "kimpass")
	get := authedClient(t, ts.URL, token)

	root := t.TempDir()
	seriesDir := filepath.Join(root, "Folder Show (2019)")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seriesDir, "theme.mp3"), []byte("theme"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	libID := createTestLibrary(t, st, "FolderShowLib", storage.CollectionTypeTVShows)
	if err := st.UpsertItem(context.Background(), storage.Item{
		Type: storage.ItemTypeSeries, LibraryID: libID, Name: "Folder Show", Path: seriesDir,
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := mustFindItemByName(t, st, libID, "Folder Show")

	resp := get(http.MethodGet, "/Items/"+string(itemID))
	defer resp.Body.Close()
	var dto baseItemDto
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		t.Fatalf("decoding item: %v", err)
	}
	if dto.Path != seriesDir {
		t.Errorf("Path = %q, want %q", dto.Path, seriesDir)
	}
	if len(dto.MediaSources) != 0 {
		t.Errorf("MediaSources = %+v, want none for a folder-shaped item", dto.MediaSources)
	}

	stream := get(http.MethodGet, "/Videos/"+string(itemID)+"/stream")
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusNotFound {
		t.Errorf("stream status = %d, want 404 for a folder-shaped item", stream.StatusCode)
	}

	theme := get(http.MethodGet, "/Items/"+string(itemID)+"/ThemeMedia")
	defer theme.Body.Close()
	var result themeMediaResponse
	if err := json.NewDecoder(theme.Body).Decode(&result); err != nil {
		t.Fatalf("decoding ThemeMedia: %v", err)
	}
	if got := result.ThemeSongsResult.TotalRecordCount; got != 1 {
		t.Errorf("ThemeSongsResult.TotalRecordCount = %d, want 1 (theme.mp3 inside the series folder)", got)
	}
}
