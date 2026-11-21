package api

import (
	"context"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A copied picture in Matroska is laid out on the file's own key frames, and
// the answer is recorded in the catalog: this walks the real path (request ->
// transcode manager -> container index -> keyframe_data) because each half is
// tested on its own and the join between them is where a mistake hides.
func TestHLSPlaylistReadsKeyFramesAndRecordsThem(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on $PATH")
	}
	src := filepath.Join(t.TempDir(), "irregular.mkv")
	gen := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=20:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=20",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "1000", "-sc_threshold", "0",
		"-force_key_frames", "0,4,11,19", "-c:a", "ac3", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate the fixture: %v\n%s", err, out)
	}

	ts, st := newTestServer(t)
	user := createTestUser(t, st, "noor", "noorpass")
	token := loginToken(t, ts.URL, user.Name, "noorpass")
	libID := createTestLibrary(t, st, "NoorLib", storage.CollectionTypeMovies)
	// the streams carry their bit rate: a request that names one cannot copy
	// a picture whose own rate is unknown
	runTicks := int64(20 * 10000000)
	item := createTestItem(t, st, storage.Item{
		Type:         storage.ItemTypeMovie,
		LibraryID:    libID,
		Name:         "Irregular Key Frames",
		Container:    "mkv",
		Path:         src,
		RunTimeTicks: &runTicks,
		MediaStreams: []storage.MediaStreamRef{
			{Type: storage.StreamTypeVideo, Codec: "h264", IsDefault: true, BitRate: int64Ptr(500000), Width: intPtr(320), Height: intPtr(240)},
			{Type: storage.StreamTypeAudio, Codec: "ac3", IsDefault: true, BitRate: int64Ptr(192000)},
		},
	})

	query := "?MediaSourceId=" + string(item.ID) +
		"&VideoCodec=h264&AudioCodec=aac&AllowVideoStreamCopy=true&EnableAutoStreamCopy=true" +
		"&RequireAvc=false&AudioStreamIndex=1&SegmentContainer=ts&MinSegments=1&VideoBitrate=999999999"
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+string(item.ID)+"/main.m3u8"+query, nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET main.m3u8: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)

	// four key frames (0, 4, ~11 and ~19 s) with a 6 s grid: the plan cuts at
	// the key frame past 6 s and at the next one, so the playlist says
	// 11.006 + 8 + 0.994 here and 11 + 8 + 1 on another ffmpeg build, where the
	// muxer rounds the key frames to the frame boundary; both follow the file
	// and neither is the grid's 6 + 6 + 6 + 2
	var durations []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#EXTINF:") {
			d, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			durations = append(durations, d)
		}
	}
	want := []float64{11, 8, 1}
	if len(durations) != len(want) {
		t.Fatalf("the playlist has %d segments, want %d (the file's key frames, not the 6 s grid): %v\n%s",
			len(durations), len(want), durations, body)
	}
	for i, d := range durations {
		got, err := strconv.ParseFloat(d, 64)
		if err != nil {
			t.Fatalf("segment %d duration %q: %v", i, d, err)
		}
		if math.Abs(got-want[i]) > 0.05 {
			t.Fatalf("segment %d lasts %.3f s, want %.0f s: the cut is not on the file's key frame: %v\n%s",
				i, got, want[i], durations, body)
		}
	}

	st2, _ := os.Stat(src)
	data, ok, err := st.QueryKeyFrames(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the key frames of the file were not recorded in the catalog")
	}
	if data.Size != st2.Size() || data.Mtime != st2.ModTime().UnixNano() {
		t.Fatalf("the record's guard is not the file: %d/%d against %d/%d",
			data.Size, data.Mtime, st2.Size(), st2.ModTime().UnixNano())
	}
	if len(data.Ticks) != 4 {
		t.Fatalf("recorded %d key frames, want 4: %v", len(data.Ticks), data.Ticks)
	}
}

// int64Ptr is the pointer form of the optional stream numbers.
func int64Ptr(n int64) *int64 { return &n }
