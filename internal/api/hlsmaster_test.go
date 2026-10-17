package api

import (
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestMainQueryGathersRepeatedNamesAndSetsAudioCodec(t *testing.T) {
	got := mainQuery("a=1&AudioCodec=aac&b=2&audiocodec=mp3&a=3", "copy")
	if want := "a=1&a=3&AudioCodec=copy&b=2"; got != want {
		t.Fatalf("mainQuery = %q, want %q", got, want)
	}
	if got := mainQuery("a=1", "aac"); got != "a=1&AudioCodec=aac" {
		t.Fatalf("absent codec not appended: %q", got)
	}
}

func TestGridDurationsAndMediaPlaylist(t *testing.T) {
	d := gridDurations(200_230_000, 6)
	if len(d) != 4 || d[3] < 2.02 || d[3] > 2.03 {
		t.Fatalf("durations = %v", d)
	}
	pl := buildMediaPlaylist(d, 6, "ts", "ApiKey=k")
	for _, want := range []string{
		"#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n",
		"#EXTINF:6.000000, nodesc\nhls1/main/0.ts?ApiKey=k&runtimeTicks=0&actualSegmentLengthTicks=60000000\n",
		"hls1/main/3.ts?ApiKey=k&runtimeTicks=180000000&actualSegmentLengthTicks=20230000\n#EXT-X-ENDLIST\n",
	} {
		if !strings.Contains(pl, want) {
			t.Fatalf("playlist lacks %q:\n%s", want, pl)
		}
	}
}

func TestBuildMasterVariantAndSubtitleGroup(t *testing.T) {
	w, h, ch := 640, 360, 2
	fps := 24.0
	lvl := 31.0
	item := storage.Item{ID: "abc", MediaStreams: []storage.MediaStreamRef{
		{Index: 0, Type: storage.StreamTypeVideo, Codec: "h264", Profile: "High", Level: &lvl, Width: &w, Height: &h, FrameRate: &fps},
		{Index: 1, Type: storage.StreamTypeAudio, Codec: "aac", Channels: &ch, IsDefault: true},
		{Index: 2, Type: storage.StreamTypeSubtitle, Codec: "subrip", Language: "eng"},
		{Index: 3, Type: storage.StreamTypeSubtitle, Codec: "pgssub", Language: "eng"},
	}}
	q := url.Values{"subtitlemethod": {"Hls"}}
	m := buildMaster(item, q, "SubtitleMethod=Hls", "key", "copy", "copy", nil, nil)
	for _, want := range []string{
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs"`, `URI="abc/Subtitles/2/subtitles.m3u8?SegmentLength=30&ApiKey=key"`,
		`BANDWIDTH=256000,AVERAGE-BANDWIDTH=256000,VIDEO-RANGE=SDR,CODECS="avc1.64001F,mp4a.40.2",RESOLUTION=640x360,FRAME-RATE=24,SUBTITLES="subs"`,
		"main.m3u8?SubtitleMethod=Hls&AudioCodec=copy",
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("master lacks %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "Subtitles/3/") {
		t.Fatalf("bitmap track listed:\n%s", m)
	}
	enc := buildMaster(item, url.Values{"maxwidth": {"300"}, "videobitrate": {"500000"}, "h264-profile": {"main|high"}, "h264-level": {"62"}}, "", "", "h264", "aac", nil, nil)
	if !strings.Contains(enc, `BANDWIDTH=756000`) || !strings.Contains(enc, `CODECS="avc1.4D4033,mp4a.40.2",RESOLUTION=300x169`) {
		t.Fatalf("encoded variant:\n%s", enc)
	}
}

func TestDefaultAudioBandwidth(t *testing.T) {
	for _, c := range []struct{ ch, src, want int }{
		{1, 1, 128000}, {2, 2, 256000}, {3, 3, 256000}, {4, 4, 256000},
		{5, 5, 768000}, {5, 6, 640000}, {6, 6, 640000}, {8, 8, 640000},
	} {
		if got := defaultAudioBandwidth(c.ch, c.src); got != c.want {
			t.Errorf("defaultAudioBandwidth(%d, %d) = %d, want %d", c.ch, c.src, got, c.want)
		}
	}
}

func TestCutDurations(t *testing.T) {
	got := cutDurations([]int64{70230000, 130230000}, 1200230000)
	want := []float64{7.023, 6, 107}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
