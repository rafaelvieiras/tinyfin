package transcode

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestKeyFramesAndSeekedCopy checks, with a real ffmpeg, the key frame scan
// and a copied job that starts mid-file: the segments are numbered from the
// start number, cut at the plan and carry the source's time plus 10 s.
func TestKeyFramesAndSeekedCopy(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on $PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe on $PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=160x120:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=500:duration=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "1000", "-sc_threshold", "0",
		"-force_key_frames", "0,7,13,21", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}

	m := New(Config{FFmpegPath: ffmpeg, FFprobePath: ffprobe, WorkDir: dir})
	kf, err := m.KeyFrames(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(kf) != 4 || math.Abs(kf[1]-7) > 0.05 || math.Abs(kf[3]-21) > 0.05 {
		t.Fatalf("key frames: %v", kf)
	}
	again, err := New(Config{FFmpegPath: ffmpeg, FFprobePath: "/nonexistent", WorkDir: dir}).KeyFrames(context.Background(), src)
	if err != nil || len(again) != 4 {
		t.Fatalf("the scan must come back from the disk cache: %v %v", again, err)
	}

	cuts := SegmentCuts(kf, 6) // 7, 13, 21 (each is past its grid line)
	if len(cuts) != 3 {
		t.Fatalf("cuts: %v", cuts)
	}
	out := filepath.Join(dir, "job")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	req := Request{InputPath: src, VideoCodec: "copy", AudioCodec: "copy", Container: "ts",
		SegmentSeconds: 6, StartTimeTicks: cuts[0], StartNumber: 1, Cuts: cuts}
	cmd := exec.Command(ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, buildArgs(req)...)...)
	cmd.Dir = out
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	start := func(name string) float64 {
		b, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=start_time", "-of", "csv=p=0", filepath.Join(out, name)).Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		return f
	}
	if _, err := os.Stat(filepath.Join(out, "seg_00000.ts")); err == nil {
		t.Fatal("a job numbers its segments from the start number")
	}
	if s := start("seg_00001.ts"); math.Abs(s-17) > 0.1 { // 7 s into the file + 10 s
		t.Fatalf("segment 1 starts at %v, want ~17", s)
	}
	if s := start("seg_00002.ts"); math.Abs(s-23) > 0.1 { // 13 s + 10 s
		t.Fatalf("segment 2 starts at %v, want ~23", s)
	}
	if s := start("seg_00003.ts"); math.Abs(s-31) > 0.1 {
		t.Fatalf("segment 3 starts at %v, want ~31", s)
	}
}
