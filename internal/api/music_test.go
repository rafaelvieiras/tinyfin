package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Music items are linked by name: a track names its artists and album artist,
// the artist listings resolve them to artist items (the folder artist when
// there is one, else the artist made from the name) and the ArtistIds /
// AlbumIds filters select by them. A track streams as the file it is.
func TestMusicLibraryBrowsing(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "muser", "mpass")
	token := loginToken(t, ts.URL, user.Name, "mpass")
	get := authedClient(t, ts.URL, token)
	ctx := context.Background()

	root := t.TempDir()
	trackPath := filepath.Join(root, "Band", "Record", "01 - Song.mp3")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := storage.Library{ID: "0123456789abcdef0123456789abcdef", Name: "Music", CollectionType: storage.CollectionTypeMusic, Paths: []string{root}}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	artistID, albumID, trackID, guestID := storage.ItemID("a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"), storage.ItemID("b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"),
		storage.ItemID("c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"), storage.ItemID("d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4")
	one, ticks := 1, int64(30_000_000)
	for _, it := range []storage.Item{
		{ID: artistID, Type: storage.ItemTypeMusicArtist, LibraryID: lib.ID, Name: "Band", SortName: "Band", Path: filepath.Join(root, "Band")},
		{ID: albumID, Type: storage.ItemTypeMusicAlbum, LibraryID: lib.ID, ParentID: &artistID, Name: "Record", SortName: "Record",
			Path: filepath.Dir(trackPath), AlbumArtist: "Band", Artists: []string{"Guest"}},
		{ID: trackID, Type: storage.ItemTypeAudio, LibraryID: lib.ID, ParentID: &albumID, Name: "Song", SortName: "Song",
			Path: trackPath, Container: "mp3", Album: "Record", AlbumArtist: "Band", Artists: []string{"Guest"},
			IndexNumber: &one, RunTimeTicks: &ticks,
			MediaStreams: []storage.MediaStreamRef{{Index: 0, Type: storage.StreamTypeAudio, Codec: "mp3"}}},
		{ID: guestID, Type: storage.ItemTypeMusicArtist, LibraryID: lib.ID, Name: "Guest", SortName: "Guest", ExtraType: storage.ExtraTypeVirtualArtist},
	} {
		if err := st.UpsertItem(ctx, it); err != nil {
			t.Fatalf("UpsertItem %s: %v", it.Name, err)
		}
	}

	var track baseItemDto
	getJSON := func(path string, into any) {
		t.Helper()
		resp := get(http.MethodGet, path)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	getJSON("/Items/"+string(trackID), &track)
	if track.MediaType != "Audio" || track.AlbumID != string(albumID) || track.Album != "Record" {
		t.Fatalf("track = %+v", track)
	}
	if len(track.ArtistItems) != 1 || track.ArtistItems[0].ID != string(guestID) {
		t.Fatalf("ArtistItems = %+v, want the virtual artist %s", track.ArtistItems, guestID)
	}
	if len(track.AlbumArtists) != 1 || track.AlbumArtists[0].ID != string(artistID) {
		t.Fatalf("AlbumArtists = %+v, want the folder artist %s", track.AlbumArtists, artistID)
	}

	var album baseItemDto
	getJSON("/Items/"+string(albumID), &album)
	if album.MediaType != "Unknown" || !album.IsFolder || album.ChildCount == nil || *album.ChildCount != 1 {
		t.Fatalf("album = %+v", album)
	}

	type list struct {
		Items            []baseItemDto
		TotalRecordCount int
	}
	var arts list
	getJSON("/Artists", &arts)
	if arts.TotalRecordCount != 1 || arts.Items[0].Name != "Guest" {
		t.Fatalf("/Artists = %+v, want the track artist only", arts)
	}
	getJSON("/Artists/AlbumArtists", &arts)
	if arts.TotalRecordCount != 1 || arts.Items[0].Name != "Band" || arts.Items[0].Type != "MusicArtist" {
		t.Fatalf("/Artists/AlbumArtists = %+v", arts)
	}

	var guest baseItemDto
	getJSON("/Items/"+string(guestID), &guest)
	if guest.SongCount == nil || *guest.SongCount != 1 || guest.AlbumCount == nil || *guest.AlbumCount != 1 {
		t.Fatalf("guest counts = %+v", guest)
	}

	var tracks list
	getJSON("/Items?Recursive=true&IncludeItemTypes=Audio&ArtistIds="+string(guestID), &tracks)
	if tracks.TotalRecordCount != 1 {
		t.Fatalf("ArtistIds filter: %d", tracks.TotalRecordCount)
	}
	getJSON("/Items?Recursive=true&IncludeItemTypes=Audio&AlbumIds="+string(albumID), &tracks)
	if tracks.TotalRecordCount != 1 {
		t.Fatalf("AlbumIds filter: %d", tracks.TotalRecordCount)
	}
	getJSON("/Items?Recursive=true&IncludeItemTypes=Audio&ArtistIds=00000000000000000000000000000001", &tracks)
	if tracks.TotalRecordCount != 0 {
		t.Fatalf("unknown artist id matched %d tracks", tracks.TotalRecordCount)
	}

	resp := get(http.MethodGet, "/Audio/"+string(trackID)+"/stream?static=true")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "0123456789" || resp.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("stream: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if r := get(http.MethodGet, "/Audio/"+string(albumID)+"/stream"); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("streaming an album = %d, want 400", r.StatusCode)
	}
}
