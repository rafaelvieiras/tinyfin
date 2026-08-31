package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// createTestItem inserts an item directly through the storage layer and
// returns the persisted copy (with its generated ID).
func createTestItem(t *testing.T, st storage.ItemStore, item storage.Item) storage.Item {
	t.Helper()
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	// UpsertItem mutates a copy internally and doesn't hand the generated
	// ID back directly, so look the item up by its distinguishing fields
	// via a bounded query instead.
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{SearchText: item.Name, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	for _, it := range page.Items {
		if it.Name == item.Name && it.Path == item.Path {
			return it
		}
	}
	t.Fatalf("could not find just-inserted item %q back via QueryItems", item.Name)
	return storage.Item{}
}

func TestPlaybackInfoDirectPlaySupported(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "gina", "ginapass")
	token := loginToken(t, ts.URL, user.Name, "ginapass")
	libID := createTestLibrary(t, st, "GinaLib", storage.CollectionTypeMovies)

	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Direct Playable Movie",
		Container: "mp4",
		Path:      "/media/direct-playable.mp4",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/Items/"+string(item.ID)+"/PlaybackInfo", nil)
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Jellyfin Web", Device="TestDevice", DeviceId="test-device-1", Version="1.0.0"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET PlaybackInfo: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got playbackInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding PlaybackInfoResponse: %v", err)
	}
	if len(got.MediaSources) != 1 {
		t.Fatalf("len(MediaSources) = %d, want 1", len(got.MediaSources))
	}
	if !got.MediaSources[0].SupportsDirectPlay {
		t.Error("SupportsDirectPlay = false, want true")
	}
	if got.MediaSources[0].Container != "mp4" {
		t.Errorf("Container = %q, want %q", got.MediaSources[0].Container, "mp4")
	}
	if got.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", got.ErrorCode)
	}
}

func TestPlaybackInfoDirectPlayUnsupported(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "hank", "hankpass")
	token := loginToken(t, ts.URL, user.Name, "hankpass")
	libID := createTestLibrary(t, st, "HankLib", storage.CollectionTypeMovies)

	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Incompatible Movie",
		Container: "mkv",
		Path:      "/media/incompatible.mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: true},
		},
	})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/Items/"+string(item.ID)+"/PlaybackInfo", nil)
	req.Header.Set("X-Emby-Token", token)
	// No Client header this time -> falls through to deviceprofile.Default(),
	// which is conservative (mp4/h264/aac only).
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET PlaybackInfo: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got playbackInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding PlaybackInfoResponse: %v", err)
	}
	if len(got.MediaSources) != 0 {
		t.Fatalf("len(MediaSources) = %d, want 0", len(got.MediaSources))
	}
	if got.ErrorCode != "NoCompatibleStream" {
		t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, "NoCompatibleStream")
	}
}

func TestPlaybackInfoPostInlineProfile(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "iris", "irispass")
	token := loginToken(t, ts.URL, user.Name, "irispass")
	libID := createTestLibrary(t, st, "IrisLib", storage.CollectionTypeMovies)

	// mkv/hevc is not in any built-in profile's baseline, but the client's
	// own inline profile (POST body) should be authoritative over any
	// registry/default fallback.
	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Custom Profile Movie",
		Container: "mkv",
		Path:      "/media/custom.mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "ac3", IsDefault: true},
		},
	})

	reqBody := playbackInfoRequestBody{
		DeviceProfile: &deviceprofile.DeviceProfile{
			Name: "Custom Client",
			DirectPlayProfiles: []deviceprofile.DirectPlayProfile{
				{Type: "Video", Container: "mkv", VideoCodec: "hevc", AudioCodec: "ac3"},
			},
		},
	}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+string(item.ID)+"/PlaybackInfo", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST PlaybackInfo: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got playbackInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding PlaybackInfoResponse: %v", err)
	}
	if len(got.MediaSources) != 1 || !got.MediaSources[0].SupportsDirectPlay {
		t.Fatalf("MediaSources = %+v, want 1 direct-playable source (inline profile should win)", got.MediaSources)
	}
}

func TestVideoStreamServesBytesAndRange(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "jack", "jackpass")
	token := loginToken(t, ts.URL, user.Name, "jackpass")
	libID := createTestLibrary(t, st, "JackLib", storage.CollectionTypeMovies)

	mediaPath := filepath.Join(t.TempDir(), "fake-media.mp4")
	content := []byte("0123456789ABCDEFGHIJ") // 20 bytes, enough to test a range
	if err := os.WriteFile(mediaPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Streamable Movie",
		Container: "mp4",
		Path:      mediaPath,
		Size:      int64(len(content)),
	})

	// Full-body GET.
	fullReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+string(item.ID)+"/stream", nil)
	fullReq.Header.Set("X-Emby-Token", token)
	fullResp, err := http.DefaultClient.Do(fullReq)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer fullResp.Body.Close()
	if fullResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", fullResp.StatusCode)
	}
	fullBody, err := io.ReadAll(fullResp.Body)
	if err != nil {
		t.Fatalf("reading full body: %v", err)
	}
	if !bytes.Equal(fullBody, content) {
		t.Errorf("full body = %q, want %q", fullBody, content)
	}

	// Ranged GET (bytes=0-4) should return a 206 Partial Content with just
	// the first 5 bytes — this is what makes seeking work for a real
	// client.
	rangeReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+string(item.ID)+"/stream.mp4", nil)
	rangeReq.Header.Set("X-Emby-Token", token)
	rangeReq.Header.Set("Range", "bytes=0-4")
	rangeResp, err := http.DefaultClient.Do(rangeReq)
	if err != nil {
		t.Fatalf("GET ranged stream: %v", err)
	}
	defer rangeResp.Body.Close()
	if rangeResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rangeResp.StatusCode)
	}
	rangeBody, err := io.ReadAll(rangeResp.Body)
	if err != nil {
		t.Fatalf("reading range body: %v", err)
	}
	if string(rangeBody) != "01234" {
		t.Errorf("range body = %q, want %q", rangeBody, "01234")
	}
}

func TestVideoStreamNotFoundForMissingItem(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "kim", "kimpass")
	token := loginToken(t, ts.URL, user.Name, "kimpass")

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/does-not-exist/stream", nil)
	req.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
