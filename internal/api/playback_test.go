package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	req.Header.Set("Authorization", `MediaBrowser Client="Jellyfin Web", Device="TestDevice", DeviceId="test-device-1", Version="1.0.0", Token="`+token+`"`)
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
	if got.MediaSources[0].Container != "mov,mp4,m4a,3gp,3g2,mj2" {
		// without a profile the source carries the format name of the probe
		t.Errorf("Container = %q, want %q", got.MediaSources[0].Container, "mov,mp4,m4a,3gp,3g2,mj2")
	}
	if got.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", got.ErrorCode)
	}
}

// TestPlaybackInfoDirectPlayUnsupportedFallsBackToTranscode covers what
// used to be Phase 1's "unsupported" case: an item incompatible with the
// resolved profile's DirectPlayProfiles. Phase 1 had no transcode ladder at
// all, so this used to report ErrorCode: NoCompatibleStream with zero
// MediaSources — now that every built-in profile (including
// deviceprofile.Default(), used here) carries a TranscodingProfiles
// fallback (internal/playback.Decide, docs/07's ladder), the same item
// correctly reaches Transcode mode instead. See
// TestPlaybackInfoNoCompatibleStreamWhenProfileHasNoTranscodingFallback
// below for the case that still produces NoCompatibleStream.
func TestPlaybackInfoDirectPlayUnsupportedFallsBackToTranscode(t *testing.T) {
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

	// A request without a device profile is direct play (the reference
	// states no limit then); the conservative deviceprofile.Default()
	// (mp4/h264/aac only, with a TranscodingProfiles fallback) is sent inline.
	prof, _ := json.Marshal(map[string]any{"DeviceProfile": deviceprofile.Default()})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+string(item.ID)+"/PlaybackInfo", bytes.NewReader(prof))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
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
	source := got.MediaSources[0]
	if source.SupportsDirectPlay {
		t.Error("SupportsDirectPlay = true, want false")
	}
	if !source.SupportsTranscoding {
		t.Error("SupportsTranscoding = false, want true")
	}
	if source.TranscodingUrl == "" {
		t.Error("TranscodingUrl is empty, want a master.m3u8 URL")
	}
	// The token must be embedded in the URL itself, not just implied by
	// the request that fetched PlaybackInfo: a browser's hls.js resolves
	// TranscodingUrl directly and can't attach the Authorization header a
	// normal API call would — confirmed by testing an actual transcode
	// through jellyfin-web in a real browser, where the master.m3u8 fetch
	// 401'd until this was added (ROADMAP.md's Phase 2 HLS serving entry).
	if !strings.Contains(source.TranscodingUrl, "ApiKey="+token) {
		t.Errorf("TranscodingUrl = %q, want it to contain ApiKey=%s", source.TranscodingUrl, token)
	}
	if source.TranscodingSubProtocol != "hls" {
		t.Errorf("TranscodingSubProtocol = %q, want %q", source.TranscodingSubProtocol, "hls")
	}
	if got.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", got.ErrorCode)
	}
}

// TestPlaybackInfoNoCompatibleStreamWhenProfileHasNoTranscodingFallback
// covers the genuine ModeNone case: a profile with no TranscodingProfiles
// entry at all leaves Decide nothing to fall back to, so PlaybackInfo must
// still report ErrorCode: NoCompatibleStream with zero MediaSources.
func TestPlaybackInfoNoCompatibleStreamWhenProfileHasNoTranscodingFallback(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "iris", "irispass")
	token := loginToken(t, ts.URL, user.Name, "irispass")
	libID := createTestLibrary(t, st, "IrisLib", storage.CollectionTypeMovies)

	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "No Fallback Movie",
		Container: "mkv",
		Path:      "/media/no-fallback.mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: true},
		},
	})

	body, err := json.Marshal(struct {
		DeviceProfile deviceprofile.DeviceProfile `json:"DeviceProfile"`
	}{
		DeviceProfile: deviceprofile.DeviceProfile{
			Name: "No Transcode Fallback",
			DirectPlayProfiles: []deviceprofile.DirectPlayProfile{
				{Type: "Video", Container: "mp4", VideoCodec: "h264"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+string(item.ID)+"/PlaybackInfo", bytes.NewReader(body))
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	req.Header.Set("Content-Type", "application/json")
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
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
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
	fullReq.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
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
	rangeReq.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
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
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestBitrateTestRoundedSize unit-tests bitrateTestRoundedSize against every
// value spread confirmed by binary search against a real Jellyfin server
// (reference server): every response length observed there was a power of
// two, with a floor of 16 and Size itself passed through unchanged whenever
// it was already an exact power of two.
func TestBitrateTestRoundedSize(t *testing.T) {
	for _, tc := range []struct {
		size int
		want int
	}{
		{1, 16}, {2, 16}, {15, 16}, {16, 16},
		{17, 32}, {31, 32}, {32, 32},
		{33, 64},
		{100, 128},
		{1000, 1024}, {1025, 2048},
		{65536, 65536},
		{100000, 131072}, {131072, 131072}, {131073, 262144},
		{500000, 524288},
		{100_000_000, 134217728},
	} {
		if got := bitrateTestRoundedSize(tc.size); got != tc.want {
			t.Errorf("bitrateTestRoundedSize(%d) = %d, want %d", tc.size, got, tc.want)
		}
	}
}

// TestPlaybackBitrateTest regression-guards GET /Playback/BitrateTest end
// to end: default size (Size omitted), an explicit Size, the rounding, the
// Content-Type, and the min/max validation bounds — all confirmed against a
// real Jellyfin server (reference server) via handlePlaybackBitrateTest's doc
// comment.
func TestPlaybackBitrateTest(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "leo", "leopass")
	token := loginToken(t, ts.URL, user.Name, "leopass")
	get := authedClient(t, ts.URL, token)

	// No Size param: same 131072 bytes a real server returns by default
	// (bitrateTestDefaultSize's doc comment).
	defaultResp := get(http.MethodGet, "/Playback/BitrateTest")
	defer defaultResp.Body.Close()
	if defaultResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", defaultResp.StatusCode)
	}
	if ct := defaultResp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	defaultBody, err := io.ReadAll(defaultResp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if len(defaultBody) != 131072 {
		t.Errorf("len(body) = %d, want 131072", len(defaultBody))
	}

	// Explicit Size, rounded up to the next power of two.
	sizedResp := get(http.MethodGet, "/Playback/BitrateTest?Size=1000")
	defer sizedResp.Body.Close()
	sizedBody, err := io.ReadAll(sizedResp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if len(sizedBody) != 1024 {
		t.Errorf("len(body) = %d, want 1024", len(sizedBody))
	}

	// Two requests for the same Size come back with different bytes —
	// confirmed against the real server, not assumed.
	sizedResp2 := get(http.MethodGet, "/Playback/BitrateTest?Size=1000")
	defer sizedResp2.Body.Close()
	sizedBody2, err := io.ReadAll(sizedResp2.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if bytes.Equal(sizedBody, sizedBody2) {
		t.Error("two BitrateTest responses for the same Size came back byte-identical, want random content each time")
	}

	// Below the minimum: 400.
	tooSmall := get(http.MethodGet, "/Playback/BitrateTest?Size=0")
	defer tooSmall.Body.Close()
	if tooSmall.StatusCode != http.StatusBadRequest {
		t.Errorf("Size=0 status = %d, want 400", tooSmall.StatusCode)
	}

	// Above the maximum: 400.
	tooBig := get(http.MethodGet, "/Playback/BitrateTest?Size=100000001")
	defer tooBig.Body.Close()
	if tooBig.StatusCode != http.StatusBadRequest {
		t.Errorf("Size=100000001 status = %d, want 400", tooBig.StatusCode)
	}

	// At the maximum: still 200.
	atMax := get(http.MethodGet, "/Playback/BitrateTest?Size=100000000")
	defer atMax.Body.Close()
	if atMax.StatusCode != http.StatusOK {
		t.Errorf("Size=100000000 status = %d, want 200", atMax.StatusCode)
	}

	// Non-integer Size: 400, not a 500 or a panic.
	notAnInt := get(http.MethodGet, "/Playback/BitrateTest?Size=abc")
	defer notAnInt.Body.Close()
	if notAnInt.StatusCode != http.StatusBadRequest {
		t.Errorf("Size=abc status = %d, want 400", notAnInt.StatusCode)
	}
}

// TestPlaybackBitrateTestRequiresAuth regression-guards that GET
// /Playback/BitrateTest needs a valid token — confirmed against a real
// Jellyfin server (reference server): a request with no token comes back
// 401.
func TestPlaybackBitrateTestRequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/Playback/BitrateTest?Size=16")
	if err != nil {
		t.Fatalf("GET /Playback/BitrateTest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
