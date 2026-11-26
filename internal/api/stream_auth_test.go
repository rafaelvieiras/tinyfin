package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// streamFixture builds a server with one movie and one audio track whose
// bytes can be served, and returns their ids.
func streamFixture(t *testing.T) (base, movieID, audioID, token string) {
	t.Helper()
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "zoe", "zoepass")
	token = loginToken(t, ts.URL, user.Name, "zoepass")

	movieLib := createTestLibrary(t, st, "ZoeMovies", storage.CollectionTypeMovies)
	moviePath := filepath.Join(t.TempDir(), "documentary.mkv")
	if err := os.WriteFile(moviePath, []byte("0123456789ABCDEFGHIJ"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	movie := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: movieLib,
		Name:      "Documentary",
		Container: "mkv",
		Path:      moviePath,
		Size:      20,
	})

	musicLib := createTestLibrary(t, st, "ZoeMusic", storage.CollectionTypeMusic)
	audioPath := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(audioPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	audio := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeAudio,
		LibraryID: musicLib,
		Name:      "Song",
		Container: "mp3",
		Path:      audioPath,
		Size:      10,
	})

	return ts.URL, string(movie.ID), string(audio.ID), token
}

// TestStreamRoutesAnswerWithoutACredential: the Android TV client's direct play
// requests the media file with no credential at all — its player's HTTP client
// carries no auth interceptor and the URL carries no key — and the reference
// answers such a request with the file. Measured on the reference: 206 for a
// static stream, 206 again with a bogus token, and 400 (not 401) for an item
// that does not exist, which is the proof that no auth check runs at all on
// this path. Requiring a token here broke every direct-play attempt from that
// client: four requests, four 401s, and a play that stopped within a second.
func TestStreamRoutesAnswerWithoutACredential(t *testing.T) {
	base, movieID, audioID, _ := streamFixture(t)

	tests := []struct {
		name     string
		path     string
		headers  map[string]string
		want     int
		wantBody string
	}{
		{name: "video stream", path: "/Videos/" + movieID + "/stream", want: http.StatusOK, wantBody: "0123456789ABCDEFGHIJ"},
		{name: "video stream with extension", path: "/Videos/" + movieID + "/stream.mkv", want: http.StatusOK},
		{name: "video stream static", path: "/Videos/" + movieID + "/stream?static=true", want: http.StatusOK},
		{name: "video stream ranged", path: "/Videos/" + movieID + "/stream", headers: map[string]string{"Range": "bytes=0-4"}, want: http.StatusPartialContent, wantBody: "01234"},
		{name: "audio stream", path: "/Audio/" + audioID + "/stream?static=true", want: http.StatusOK, wantBody: "0123456789"},
		{
			// An unusable credential must not turn a request the reference
			// serves into a refusal: the player only ever sees "it worked".
			name:    "video stream with a stale token",
			path:    "/Videos/" + movieID + "/stream",
			headers: map[string]string{"Authorization": `MediaBrowser Token="no-longer-valid"`},
			want:    http.StatusOK,
		},
		{
			name: "video stream with a bogus api key",
			path: "/Videos/" + movieID + "/stream?ApiKey=not-a-key",
			want: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, base+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.wantBody != "" {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("reading body: %v", err)
				}
				if string(body) != tc.wantBody {
					t.Errorf("body = %q, want %q", body, tc.wantBody)
				}
			}
		})
	}
}

// TestOnlyTheStreamRoutesAreAnonymous pins the width of the exemption. The
// reference serves exactly /Videos/{id}/stream[.ext] and /Audio/{id}/stream[.ext]
// anonymously; every neighbour measured 401 without a credential — the HLS
// playlist and segments, the trickplay sheets, AdditionalParts, the item
// download, and the audio's universal (transcoding) route. A wildcard route
// that answers anonymously is only safe if what it does *not* cover is
// asserted, so this test exists next to the one above.
func TestOnlyTheStreamRoutesAreAnonymous(t *testing.T) {
	base, movieID, audioID, token := streamFixture(t)

	paths := []string{
		"/Videos/" + movieID + "/master.m3u8",
		"/Videos/" + movieID + "/main.m3u8",
		"/Videos/" + movieID + "/hls1/main/0.ts",
		"/Videos/" + movieID + "/seg_00000.ts",
		"/Videos/" + movieID + "/Trickplay/320/tiles.m3u8",
		"/Videos/" + movieID + "/AdditionalParts",
		"/Videos/" + movieID + "/" + movieID + "/Subtitles/0/Stream.srt",
		"/Items/" + movieID + "/Download",
		"/Items/" + movieID + "/PlaybackInfo",
		"/Items/" + movieID,
		"/Audio/" + audioID + "/universal",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			resp, err := http.Get(base + p)
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET %s without a credential = %d, want 401", p, resp.StatusCode)
			}

			req, err := http.NewRequest(http.MethodGet, base+p, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
			authed, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s with a token: %v", p, err)
			}
			defer authed.Body.Close()
			if authed.StatusCode == http.StatusUnauthorized {
				t.Errorf("GET %s with a valid token = 401, want it to pass the auth gate", p)
			}
		})
	}
}

// TestStreamStillWorksWithAToken: the exemption must not disturb the clients
// that do send a key in the URL — jellyfin-web puts one there.
func TestStreamStillWorksWithAToken(t *testing.T) {
	base, movieID, _, token := streamFixture(t)

	req, err := http.NewRequest(http.MethodGet, base+"/Videos/"+movieID+"/stream?static=true", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != "0123456789ABCDEFGHIJ" {
		t.Errorf("body = %q, want the file's bytes", body)
	}
}
