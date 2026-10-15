package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestTrickplayRoutesAndField(t *testing.T) {
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")
	get := authedClient(t, ts.URL, token)
	ctx := context.Background()
	libID := createTestLibrary(t, st, "Movies", storage.CollectionTypeMovies)

	dir := t.TempDir()
	file := filepath.Join(dir, "Clip (2020).mp4")
	sheets := filepath.Join(dir, "Clip (2020).trickplay", "320 - 10x10")
	if err := os.MkdirAll(sheets, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, filepath.Join(sheets, "0.jpg"), filepath.Join(sheets, "1.jpg")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := storage.ItemID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := st.UpsertItem(ctx, storage.Item{ID: id, Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Clip", Path: file}); err != nil {
		t.Fatal(err)
	}
	bare := storage.ItemID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err := st.UpsertItem(ctx, storage.Item{ID: bare, Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Bare", Path: filepath.Join(dir, "Bare.mp4")}); err != nil {
		t.Fatal(err)
	}
	info := storage.TrickplayInfo{Width: 320, Height: 240, TileWidth: 10, TileHeight: 10, ThumbnailCount: 150, Interval: 10000, Bandwidth: 1234}
	if err := st.UpsertTrickplayInfo(ctx, id, info); err != nil {
		t.Fatal(err)
	}

	body := func(resp *http.Response) string {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	// The item field.
	resp := get(http.MethodGet, "/Items/"+string(id))
	var dto struct {
		Trickplay map[string]map[string]trickplayDto `json:"Trickplay"`
	}
	if err := json.Unmarshal([]byte(body(resp)), &dto); err != nil {
		t.Fatal(err)
	}
	if got := dto.Trickplay[string(id)]["320"]; got != (trickplayDto{320, 240, 10, 10, 150, 10000, 1234}) {
		t.Errorf("Trickplay = %+v", dto.Trickplay)
	}
	if b := body(get(http.MethodGet, "/Items/"+string(bare))); !strings.Contains(b, `"Trickplay":{}`) {
		t.Errorf("an item without sheets must carry an empty Trickplay object: %s", b)
	}

	// The playlist.
	resp = get(http.MethodGet, "/Videos/"+string(id)+"/Trickplay/320/tiles.m3u8")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/vnd.apple.mpegurl") {
		t.Fatalf("tiles.m3u8: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	pl := body(resp)
	for _, want := range []string{"#EXT-X-TARGETDURATION:2\n", "#EXT-X-IMAGES-ONLY\n", "#EXTINF:1000,\n", "#EXTINF:500,\n",
		"#EXT-X-TILES:RESOLUTION=320x240,LAYOUT=10x10,DURATION=10\n1.jpg?MediaSourceId=" + string(id) + "&ApiKey=" + token, "#EXT-X-ENDLIST"} {
		if !strings.Contains(pl, want) {
			t.Errorf("playlist lacks %q:\n%s", want, pl)
		}
	}

	// The sheets and the failures.
	for path, want := range map[string]int{
		"/Videos/" + string(id) + "/Trickplay/320/0.jpg":                    200,
		"/Videos/" + string(id) + "/Trickplay/320/2.jpg":                    404,
		"/Videos/" + string(id) + "/Trickplay/320/-1.jpg":                   404,
		"/Videos/" + string(id) + "/Trickplay/999/0.jpg":                    404,
		"/Videos/" + string(id) + "/Trickplay/abc/0.jpg":                    400,
		"/Videos/" + string(id) + "/Trickplay/320/abc.jpg":                  400,
		"/Videos/" + string(id) + "/Trickplay/320/0.jpg?MediaSourceId=zzz":  400,
		"/Videos/" + string(bare) + "/Trickplay/320/tiles.m3u8":             404,
		"/Videos/00000000000000000000000000000001/Trickplay/320/tiles.m3u8": 404,
	} {
		r := get(http.MethodGet, path)
		r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, r.StatusCode, want)
		}
	}
	r, err := http.Get(ts.URL + "/Videos/" + string(id) + "/Trickplay/320/0.jpg")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", r.StatusCode)
	}
}
