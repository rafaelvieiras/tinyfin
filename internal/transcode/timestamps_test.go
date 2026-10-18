package transcode

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSegmentTimestampsLiftNegativeStarts checks, with a real ffmpeg, that the
// 10 s shift of the segments comes on top of a source whose first sound is
// ahead of its first picture (the AAC priming of an MP4): the sound lands on
// 10 s exactly and the picture a few milliseconds later, the way the
// reference writes them, instead of the sound at 9.98 s.
func TestSegmentTimestampsLiftNegativeStarts(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on $PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe on $PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=160x120:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=500:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	first := func(file, stream string) float64 {
		b, err := exec.Command(ffprobe, "-v", "error", "-select_streams", stream,
			"-show_entries", "packet=pts_time", "-of", "csv=p=0", file).Output()
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		lowest := math.Inf(1)
		for _, l := range strings.Fields(string(b)) {
			if f, err := strconv.ParseFloat(strings.TrimSuffix(l, ","), 64); err == nil && f < lowest {
				lowest = f
			}
		}
		return lowest
	}
	// the hls muxer (encoded or copied without a key frame plan) and the
	// segment muxer (a copied picture with its plan) write the same times
	for _, req := range []Request{
		{InputPath: src, VideoCodec: "copy", AudioCodec: "copy", Container: "ts", SegmentSeconds: 6},
		{InputPath: src, VideoCodec: "copy", AudioCodec: "copy", Container: "ts", SegmentSeconds: 6, Cuts: []int64{}},
	} {
		out := filepath.Join(dir, "job"+strconv.Itoa(len(req.Cuts)))
		if req.Cuts == nil {
			out = filepath.Join(dir, "jobnil")
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, buildArgs(req)...)...)
		cmd.Dir = out
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, b)
		}
		seg := filepath.Join(out, "seg_00000.ts")
		if a := first(seg, "a"); math.Abs(a-10) > 0.002 {
			t.Errorf("cuts=%v: first sound at %v, want 10", req.Cuts, a)
		}
		if v := first(seg, "v"); v < 10 || v > 10.05 {
			t.Errorf("cuts=%v: first picture at %v, want just after 10", req.Cuts, v)
		}
	}
}
