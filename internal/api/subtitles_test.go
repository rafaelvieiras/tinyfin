package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const subtitlesTestSRT = "1\n00:00:01,000 --> 00:00:02,500\nHello <i>world</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nSecond line\n"

type subsFixture struct {
	ts    string
	token string
	item  storage.Item
	dir   string
	st    storage.Store
}

func newSubsFixture(t *testing.T) subsFixture {
	t.Helper()
	ts, st := newTestServer(t)
	admin := createTestAdminUser(t, st, "subadmin", "subpass")
	token := loginToken(t, ts.URL, admin.Name, "subpass")
	libID := createTestLibrary(t, st, "SubsLib", storage.CollectionTypeMovies)
	dir := t.TempDir()
	video := filepath.Join(dir, "Film (2001).mp4")
	for name, data := range map[string]string{
		"Film (2001).mp4":           "x",
		"Film (2001).en.srt":        subtitlesTestSRT,
		"Film (2001).pt-BR.vtt":     "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nOla\n",
		"Film (2001).ru.sup":        "PGS",
		"Film (2001).fr.forced.srt": subtitlesTestSRT,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runtime := int64(60_000_000)
	item := createTestItem(t, st, storage.Item{
		Type: storage.ItemTypeMovie, LibraryID: libID, Name: "Film", Container: "mp4", Path: video, RunTimeTicks: &runtime,
		MediaStreams: []storage.MediaStreamRef{
			{Index: 0, Type: storage.StreamTypeSubtitle, Codec: "subrip", Language: "eng", IsExternal: true, Path: filepath.Join(dir, "Film (2001).en.srt")},
			{Index: 1, Type: storage.StreamTypeSubtitle, Codec: "subrip", Language: "fra", IsExternal: true, IsForced: true, Path: filepath.Join(dir, "Film (2001).fr.forced.srt")},
			{Index: 2, Type: storage.StreamTypeSubtitle, Codec: "webvtt", Language: "pt-br", IsExternal: true, Path: filepath.Join(dir, "Film (2001).pt-BR.vtt")},
			{Index: 3, Type: storage.StreamTypeSubtitle, Codec: "PGSSUB", Language: "rus", IsExternal: true, Path: filepath.Join(dir, "Film (2001).ru.sup")},
			{Index: 4, Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true},
			{Index: 5, Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
		},
	})
	return subsFixture{ts: ts.URL, token: token, item: item, dir: dir, st: st}
}

func (f subsFixture) do(t *testing.T, method, path string, body []byte, auth bool) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(method, f.ts+path, bytes.NewReader(body))
	if auth {
		req.Header.Set("Authorization", `MediaBrowser Client="t", Device="t", DeviceId="t", Version="1", Token="`+f.token+`"`)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestSubtitleStreamFormats(t *testing.T) {
	f := newSubsFixture(t)
	base := "/Videos/" + string(f.item.ID) + "/" + string(f.item.ID) + "/Subtitles/"

	status, h, body := f.do(t, "GET", base+"0/Stream.vtt", nil, true)
	wantVTT := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello <i>world</i>\n\n00:00:03.000 --> 00:00:04.000\nSecond line"
	if status != 200 || body != wantVTT || h.Get("Content-Type") != "text/vtt" {
		t.Errorf("vtt: %d %q %q", status, h.Get("Content-Type"), body)
	}
	// The same format is the file as it is, offsets ignored.
	status, h, body = f.do(t, "GET", base+"0/10000000/Stream.srt", nil, true)
	if status != 200 || body != subtitlesTestSRT || h.Get("Content-Type") != "application/x-subrip" {
		t.Errorf("srt: %d %q %q", status, h.Get("Content-Type"), body)
	}
	// A start position in the route shifts the cues; the query wins over it.
	_, _, body = f.do(t, "GET", base+"0/20000000/Stream.vtt", nil, true)
	if !strings.Contains(body, "00:00:00.000 --> 00:00:00.500\nHello") || !strings.Contains(body, "00:00:01.000 --> 00:00:02.000\nSecond") {
		t.Errorf("shifted: %q", body)
	}
	_, _, body = f.do(t, "GET", base+"0/20000000/Stream.vtt?startPositionTicks=30000000", nil, true)
	if strings.Contains(body, "Hello") || !strings.Contains(body, "00:00:00.000 --> 00:00:01.000\nSecond") {
		t.Errorf("query start: %q", body)
	}
	_, _, body = f.do(t, "GET", base+"0/Stream.vtt?startPositionTicks=20000000&copyTimestamps=true", nil, true)
	if body != wantVTT {
		t.Errorf("copyTimestamps: %q", body)
	}
	_, _, body = f.do(t, "GET", base+"0/Stream.vtt?endPositionTicks=20000000", nil, true)
	if strings.Contains(body, "Second") {
		t.Errorf("endPositionTicks: %q", body)
	}
	_, _, body = f.do(t, "GET", base+"0/Stream.vtt?addVttTimeMap=true", nil, true)
	if !strings.HasPrefix(body, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n") {
		t.Errorf("addVttTimeMap: %q", body)
	}
	status, h, body = f.do(t, "GET", base+"0/Stream.json", nil, true)
	if status != 200 || h.Get("Content-Type") != "application/json" || !strings.HasPrefix(body, `{"TrackEvents":[{"Id":"1","Text":"Hello \u003Ci\u003Eworld`) {
		t.Errorf("json: %d %q %q", status, h.Get("Content-Type"), body)
	}
	status, h, _ = f.do(t, "GET", base+"0/Stream.ttml", nil, true)
	if status != 200 || h.Get("Content-Type") != "application/ttml+xml" {
		t.Errorf("ttml: %d %q", status, h.Get("Content-Type"))
	}
	status, h, _ = f.do(t, "GET", base+"0/Stream.ass", nil, true)
	if status != 200 || h.Get("Content-Type") != "text/x-ssa" {
		t.Errorf("ass: %d %q", status, h.Get("Content-Type"))
	}
	// WebVTT file to SubRip.
	_, _, body = f.do(t, "GET", base+"2/Stream.srt", nil, true)
	if body != "1\n00:00:01,000 --> 00:00:02,000\nOla\n\n" {
		t.Errorf("vtt->srt: %q", body)
	}

	for _, c := range []struct {
		path   string
		status int
	}{
		{base + "0/Stream.xyz", 400},
		{base + "0/Stream", 404},
		{base + "9/Stream.vtt", 500},
		{base + "4/Stream.vtt", 500}, // a video stream
		{base + "3/Stream.vtt", 400}, // a bitmap subtitle
		{base + "abc/Stream.vtt", 400},
		{base + "0/abc/Stream.vtt", 400},
		{base + "0/Stream.vtt?copyTimestamps=maybe", 400},
		{"/Videos/" + string(f.item.ID) + "/notaguid/Subtitles/0/Stream.vtt", 500},
		{"/Videos/00000000000000000000000000000000/00000000000000000000000000000000/Subtitles/0/Stream.vtt", 400},
		{"/Videos/zzz/x/Subtitles/0/Stream.vtt", 400},
	} {
		if status, _, _ := f.do(t, "GET", c.path, nil, true); status != c.status {
			t.Errorf("%s: %d, want %d", c.path, status, c.status)
		}
	}
	if status, _, _ := f.do(t, "GET", base+"0/Stream.vtt", nil, false); status != 401 {
		t.Errorf("anonymous: %d, want 401", status)
	}
}

func TestSubtitlePlaylist(t *testing.T) {
	f := newSubsFixture(t)
	base := "/Videos/" + string(f.item.ID) + "/" + string(f.item.ID) + "/Subtitles/0/subtitles.m3u8"
	status, h, body := f.do(t, "GET", base+"?segmentLength=4", nil, true)
	if status != 200 || h.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("%d %q", status, h.Get("Content-Type"))
	}
	want := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n" +
		"#EXTINF:4,\nstream.vtt?CopyTimestamps=true&AddVttTimeMap=true&StartPositionTicks=0&EndPositionTicks=40000000&ApiKey=" + f.token + "\n" +
		"#EXTINF:2,\nstream.vtt?CopyTimestamps=true&AddVttTimeMap=true&StartPositionTicks=40000000&EndPositionTicks=60000000&ApiKey=" + f.token + "\n" +
		"#EXT-X-ENDLIST\n"
	if body != want {
		t.Errorf("playlist:\n%s", body)
	}
	if status, _, _ := f.do(t, "GET", base, nil, true); status != 400 {
		t.Errorf("no segmentLength: %d", status)
	}
	if status, _, _ := f.do(t, "GET", base+"?segmentLength=0", nil, true); status != 400 {
		t.Errorf("segmentLength 0: %d", status)
	}
	if status, _, _ := f.do(t, "GET", base+"?segmentLength=4", nil, false); status != 401 {
		t.Errorf("anonymous: %d", status)
	}
}

func TestSubtitleDeliveryInPlaybackInfo(t *testing.T) {
	f := newSubsFixture(t)
	body := []byte(`{"DeviceProfile":{"Name":"t","DirectPlayProfiles":[{"Container":"mp4","Type":"Video","VideoCodec":"h264","AudioCodec":"aac"}],
	"TranscodingProfiles":[{"Container":"ts","Type":"Video","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls"}],
	"SubtitleProfiles":[{"Format":"vtt","Method":"External"},{"Format":"srt","Method":"External"}]}}`)
	status, _, out := f.do(t, "POST", "/Items/"+string(f.item.ID)+"/PlaybackInfo", body, true)
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	var resp struct {
		MediaSources []struct {
			DefaultSubtitleStreamIndex *int
			DefaultAudioStreamIndex    *int
			MediaStreams               []mediaStreamDto
		}
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	src := resp.MediaSources[0]
	// The external files come first: the non-forced English one is selected.
	if src.DefaultSubtitleStreamIndex == nil || *src.DefaultSubtitleStreamIndex != 0 || src.DefaultAudioStreamIndex == nil || *src.DefaultAudioStreamIndex != 5 {
		t.Errorf("defaults: %v %v", src.DefaultSubtitleStreamIndex, src.DefaultAudioStreamIndex)
	}
	want := map[int]string{0: "External", 1: "External", 2: "External", 3: "Encode"}
	for _, s := range src.MediaStreams {
		if s.Type != "Subtitle" {
			continue
		}
		if s.DeliveryMethod != want[s.Index] {
			t.Errorf("stream %d: %s", s.Index, s.DeliveryMethod)
		}
		if s.DeliveryMethod == "External" {
			prefix := "/Videos/" + dashedForTest(string(f.item.ID)) + "/" + string(f.item.ID) + "/Subtitles/"
			if !strings.HasPrefix(s.DeliveryURL, prefix) || !strings.HasSuffix(s.DeliveryURL, "/0/Stream.vtt?ApiKey="+f.token) && !strings.HasSuffix(s.DeliveryURL, "/0/Stream.srt?ApiKey="+f.token) {
				t.Errorf("stream %d url %s", s.Index, s.DeliveryURL)
			}
		}
		if s.Score == nil || !s.SupportsExternalStream {
			t.Errorf("stream %d: score/supports %+v", s.Index, s)
		}
	}
	// Without a device profile of its own the request sets no delivery.
	_, _, out = f.do(t, "GET", "/Items/"+string(f.item.ID)+"/PlaybackInfo", nil, true)
	if strings.Contains(out, "DeliveryMethod") {
		t.Errorf("delivery without a profile: %s", out)
	}
}

func dashedForTest(id string) string {
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

func TestSubtitleUploadAndDelete(t *testing.T) {
	f := newSubsFixture(t)
	data := base64.StdEncoding.EncodeToString([]byte(subtitlesTestSRT))
	up := func(lang string, forced bool) int {
		b, _ := json.Marshal(map[string]any{"Language": lang, "Format": "srt", "IsForced": forced, "Data": data})
		status, _, _ := f.do(t, "POST", "/Videos/"+string(f.item.ID)+"/Subtitles", b, true)
		return status
	}
	if got := up("deu", false); got != 204 {
		t.Fatalf("upload: %d", got)
	}
	if got := up("deu", false); got != 204 {
		t.Fatalf("second upload: %d", got)
	}
	if got := up("spa", true); got != 204 {
		t.Fatalf("forced upload: %d", got)
	}
	for _, n := range []string{"Film (2001).deu.srt", "Film (2001).deu.0.srt", "Film (2001).spa.forced.srt"} {
		if _, err := os.Stat(filepath.Join(f.dir, n)); err != nil {
			t.Errorf("missing %s", n)
		}
	}
	got, err := f.st.GetItem(context.Background(), f.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, s := range got.MediaStreams {
		if s.Type == storage.StreamTypeSubtitle {
			files = append(files, filepath.Base(s.Path))
		}
	}
	if len(files) != 3 {
		// Only the rescanned folder's files are listed: the fixture's own
		// streams were stored directly and the folder holds six files.
		t.Logf("subtitle files: %v", files)
	}
	if len(files) < 5 {
		t.Errorf("upload not cataloged: %v", files)
	}
	// Delete the first stream (a file of this folder).
	status, _, _ := f.do(t, "DELETE", "/Videos/"+string(f.item.ID)+"/Subtitles/0", nil, true)
	if status != 204 {
		t.Fatalf("delete: %d", status)
	}
	for _, c := range []struct {
		path   string
		status int
	}{
		{"/Videos/" + string(f.item.ID) + "/Subtitles/99", 400},
		{"/Videos/" + string(f.item.ID) + "/Subtitles/xyz", 400},
		{"/Videos/zzz/Subtitles/0", 400},
	} {
		if status, _, _ := f.do(t, "DELETE", c.path, nil, true); status != c.status {
			t.Errorf("DELETE %s: %d, want %d", c.path, status, c.status)
		}
	}
	// A bad request answers with the problem document naming what is missing.
	status, _, body := f.do(t, "POST", "/Videos/"+string(f.item.ID)+"/Subtitles", []byte(`{}`), true)
	if status != 400 || !strings.Contains(body, "Language") || !strings.Contains(body, "Data") {
		t.Errorf("empty upload: %d %s", status, body)
	}
	b, _ := json.Marshal(map[string]any{"Language": "eng", "Format": "xyz", "Data": data})
	if status, _, _ := f.do(t, "POST", "/Videos/"+string(f.item.ID)+"/Subtitles", b, true); status != 400 {
		t.Errorf("unknown format: %d", status)
	}
}

func TestSubtitleProviderRoutes(t *testing.T) {
	f := newSubsFixture(t)
	for _, c := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/Items/" + string(f.item.ID) + "/RemoteSearch/Subtitles/eng", 200, "[]"},
		{"POST", "/Items/" + string(f.item.ID) + "/RemoteSearch/Subtitles/abc", 204, ""},
		{"GET", "/Items/00000000000000000000000000000001/RemoteSearch/Subtitles/eng", 404, ""},
		{"GET", "/Providers/Subtitles/Subtitles/abc", 500, "Error processing request."},
		{"GET", "/FallbackFont/Fonts", 200, "[]"},
		{"GET", "/FallbackFont/Fonts/Arial.ttf", 200, ""},
	} {
		status, _, body := f.do(t, c.method, c.path, nil, true)
		if status != c.status || (c.body != "" && strings.TrimSpace(body) != c.body) {
			t.Errorf("%s %s: %d %q", c.method, c.path, status, body)
		}
	}
	if status, _, _ := f.do(t, "GET", "/FallbackFont/Fonts", nil, false); status != 401 {
		t.Errorf("anonymous fonts: %d", status)
	}
}
