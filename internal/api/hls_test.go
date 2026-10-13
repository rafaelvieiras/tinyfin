package api

import (
	"bufio"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// TestHLSTranscodeEndToEnd exercises the full Phase 2 path against a real
// ffmpeg binary, if one happens to be available on this machine: an item
// declared incompatible with the resolved profile's Direct Play baseline
// (forcing Decide into ModeTranscode) is played back via
// master.m3u8 -> main.m3u8 -> a segment file, mirroring exactly what a
// real HLS client (ExoPlayer, hls.js) does. It must never fail or block
// when ffmpeg is absent — skip, don't fail — matching
// internal/transcode's own TestSmoke_RealFFmpeg convention.
func TestHLSTranscodeEndToEnd(t *testing.T) {
	ffmpegPath, err := exec.LookPath("jellyfin-ffmpeg")
	if err != nil {
		ffmpegPath, err = exec.LookPath("ffmpeg")
	}
	if err != nil {
		t.Skip("no ffmpeg/jellyfin-ffmpeg on $PATH, skipping HLS end-to-end test")
	}

	srcDir := t.TempDir()
	input := filepath.Join(srcDir, "src.mp4")
	gen := exec.Command(ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=2",
		"-shortest", "-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-c:a", "aac",
		input,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate synthetic test input with this ffmpeg build, skipping: %v\n%s", err, out)
	}

	ts, st := newTestServer(t)
	user := createTestUser(t, st, "juno", "junopass")
	token := loginToken(t, ts.URL, user.Name, "junopass")
	libID := createTestLibrary(t, st, "JunoLib", storage.CollectionTypeMovies)

	// Declared as mkv/hevc/dts — outside deviceprofile.Default()'s Direct
	// Play baseline (mp4/h264/aac) — so Decide falls all the way to
	// ModeTranscode (internal/playback/decision.go). Path points at the
	// real generated input above; the declared metadata only drives the
	// decision, not what ffmpeg actually reads.
	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "HLS Test Movie",
		Container: "mkv",
		Path:      input,
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: true},
		},
	})

	authedGet := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	masterResp := authedGet("/Videos/" + string(item.ID) + "/master.m3u8")
	defer masterResp.Body.Close()
	if masterResp.StatusCode != http.StatusOK {
		t.Fatalf("master.m3u8 status = %d, want 200", masterResp.StatusCode)
	}
	masterBody, err := io.ReadAll(masterResp.Body)
	if err != nil {
		t.Fatalf("read master.m3u8 body: %v", err)
	}
	if !strings.Contains(string(masterBody), "main.m3u8") {
		t.Fatalf("master.m3u8 body doesn't reference main.m3u8:\n%s", masterBody)
	}
	if ct := masterResp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("master.m3u8 Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}

	// handleHLSPlaylist (hls.go) blocks internally (up to
	// hlsPlaylistWaitTimeout) until jellyfin-ffmpeg has written at least
	// one segment, so a single request is enough — no polling loop needed
	// here.
	playlistResp := authedGet("/Videos/" + string(item.ID) + "/main.m3u8")
	defer playlistResp.Body.Close()
	if playlistResp.StatusCode != http.StatusOK {
		t.Fatalf("main.m3u8 status = %d, want 200", playlistResp.StatusCode)
	}
	if ct := playlistResp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("main.m3u8 Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}

	var segmentName string
	scanner := bufio.NewScanner(playlistResp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasSuffix(line, ".ts") {
			segmentName = line
			break
		}
	}
	if segmentName == "" {
		t.Fatal("main.m3u8 listed no .ts segment")
	}
	// Confirm the playlist references the segment by plain relative name,
	// not an absolute filesystem path — see internal/transcode's
	// buildArgs doc comment for why this matters: a real HLS client
	// resolves segment URIs relative to main.m3u8's own URL.
	if strings.Contains(segmentName, "/") {
		t.Fatalf("segment reference %q is not a plain relative filename", segmentName)
	}

	segResp := authedGet("/Videos/" + string(item.ID) + "/" + segmentName)
	defer segResp.Body.Close()
	if segResp.StatusCode != http.StatusOK {
		t.Fatalf("segment %s status = %d, want 200", segmentName, segResp.StatusCode)
	}
	if ct := segResp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("segment Content-Type = %q, want video/mp2t", ct)
	}
	segBody, err := io.ReadAll(segResp.Body)
	if err != nil {
		t.Fatalf("read segment body: %v", err)
	}
	if len(segBody) == 0 {
		t.Fatal("segment body is empty")
	}
}

// TestHLSTranscodeQueryOnlyAuth mirrors what a browser's hls.js actually
// does, unlike TestHLSTranscodeEndToEnd's authedGet helper: it resolves
// TranscodingUrl and every playlist-referenced segment URI itself, with no
// way to attach the Authorization header a normal API call would carry. A
// real end-to-end run through jellyfin-web in an actual browser caught
// this failing with 401 on master.m3u8 before hlsMasterURL/
// handleHLSPlaylist were fixed to embed and propagate the token via query
// string alone (ROADMAP.md's Phase 2 HLS serving entry) — this test
// exercises that same query-only path without needing a real browser.
func TestHLSTranscodeQueryOnlyAuth(t *testing.T) {
	ffmpegPath, err := exec.LookPath("jellyfin-ffmpeg")
	if err != nil {
		ffmpegPath, err = exec.LookPath("ffmpeg")
	}
	if err != nil {
		t.Skip("no ffmpeg/jellyfin-ffmpeg on $PATH, skipping HLS query-only-auth test")
	}

	srcDir := t.TempDir()
	input := filepath.Join(srcDir, "src.mp4")
	gen := exec.Command(ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=2",
		"-shortest", "-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-c:a", "aac",
		input,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate synthetic test input with this ffmpeg build, skipping: %v\n%s", err, out)
	}

	ts, st := newTestServer(t)
	user := createTestUser(t, st, "priya", "priyapass")
	token := loginToken(t, ts.URL, user.Name, "priyapass")
	libID := createTestLibrary(t, st, "PriyaLib", storage.CollectionTypeMovies)

	item := createTestItem(t, st, storage.Item{
		Type:      storage.ItemTypeMovie,
		LibraryID: libID,
		Name:      "Query Auth Test Movie",
		Container: "mkv",
		Path:      input,
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "hevc", IsDefault: true},
			{Type: storage.StreamTypeAudio, Codec: "dts", IsDefault: true},
		},
	})

	// No Authorization header anywhere below — only whatever query string
	// each response itself hands back, exactly like hls.js resolving URLs
	// out of a playlist it doesn't know is "authenticated" at all.
	get := func(rawURL string) *http.Response {
		t.Helper()
		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET %s: %v", rawURL, err)
		}
		return resp
	}

	masterResp := get(ts.URL + "/Videos/" + string(item.ID) + "/master.m3u8?ApiKey=" + token)
	defer masterResp.Body.Close()
	if masterResp.StatusCode != http.StatusOK {
		t.Fatalf("master.m3u8 status = %d, want 200", masterResp.StatusCode)
	}
	masterBody, err := io.ReadAll(masterResp.Body)
	if err != nil {
		t.Fatalf("read master.m3u8 body: %v", err)
	}
	var playlistLine string
	scanner := bufio.NewScanner(strings.NewReader(string(masterBody)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "main.m3u8") {
			playlistLine = line
			break
		}
	}
	if playlistLine == "" {
		t.Fatalf("master.m3u8 body has no main.m3u8 line:\n%s", masterBody)
	}
	if !strings.Contains(playlistLine, "ApiKey="+token) {
		t.Fatalf("main.m3u8 line %q doesn't carry the token forward", playlistLine)
	}

	playlistResp := get(ts.URL + "/Videos/" + string(item.ID) + "/" + playlistLine)
	defer playlistResp.Body.Close()
	if playlistResp.StatusCode != http.StatusOK {
		t.Fatalf("main.m3u8 status = %d, want 200", playlistResp.StatusCode)
	}
	playlistBody, err := io.ReadAll(playlistResp.Body)
	if err != nil {
		t.Fatalf("read main.m3u8 body: %v", err)
	}
	var segmentLine string
	scanner = bufio.NewScanner(strings.NewReader(string(playlistBody)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, ".ts") {
			segmentLine = line
			break
		}
	}
	if segmentLine == "" {
		t.Fatalf("main.m3u8 listed no .ts segment:\n%s", playlistBody)
	}
	if !strings.Contains(segmentLine, "ApiKey="+token) {
		t.Fatalf("segment line %q doesn't carry the token forward", segmentLine)
	}

	segResp := get(ts.URL + "/Videos/" + string(item.ID) + "/" + segmentLine)
	defer segResp.Body.Close()
	if segResp.StatusCode != http.StatusOK {
		t.Fatalf("segment status = %d, want 200 (query-string-only auth must work, no header at all)", segResp.StatusCode)
	}
}

// TestHLSMasterNotFoundForDirectPlayableItem confirms master.m3u8 refuses
// to start a transcode job for an item Decide would send through Direct
// Play instead — the HLS routes exist only for the other three modes
// (playback.go's handleVideoStream serves Direct Play directly).
func TestHLSMasterNotFoundForDirectPlayableItem(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "kian", "kianpass")
	token := loginToken(t, ts.URL, user.Name, "kianpass")
	libID := createTestLibrary(t, st, "KianLib", storage.CollectionTypeMovies)

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

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+string(item.ID)+"/master.m3u8", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET master.m3u8: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
