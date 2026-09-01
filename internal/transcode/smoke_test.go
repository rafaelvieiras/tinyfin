package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSmoke_RealFFmpeg exercises the full path end to end against a real
// ffmpeg binary, if one happens to be available on this machine — a
// synthetic input (ffmpeg's own lavfi test source, not a fixture checked
// into the repo) transcoded to a tiny H.264/AAC HLS stream. It must never
// fail or block when ffmpeg is absent: skip, don't fail, so the rest of
// the suite (and CI/dev environments without ffmpeg installed) is
// unaffected.
func TestSmoke_RealFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("jellyfin-ffmpeg")
	if err != nil {
		ffmpegPath, err = exec.LookPath("ffmpeg")
	}
	if err != nil {
		t.Skip("no ffmpeg/jellyfin-ffmpeg on $PATH, skipping smoke test")
	}

	srcDir := t.TempDir()
	input := filepath.Join(srcDir, "src.mp4")
	gen := exec.Command(ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=2",
		"-shortest",
		// yuv420p: ordinary consumer video is 4:2:0 chroma-subsampled;
		// without forcing it, lavfi's testsrc defaults to 4:4:4, which
		// H.264 High profile (what buildArgs targets for an "h264" Request
		// below) can't encode — this keeps the synthetic input
		// representative of what this package actually needs to handle.
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-c:a", "aac",
		input,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate synthetic test input with this ffmpeg build, skipping smoke test: %v\n%s", err, out)
	}

	work := t.TempDir()
	m := New(Config{FFmpegPath: ffmpegPath, WorkDir: work})

	req := Request{
		InputPath:      input,
		VideoCodec:     "h264",
		AudioCodec:     "aac",
		Container:      "ts",
		MaxHeight:      240,
		SegmentSeconds: 1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	job, err := m.Start(ctx, "smoke-session", req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop("smoke-session")

	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, statErr := os.Stat(job.PlaylistPath()); statErr == nil {
			break
		}
		if job.Done() {
			t.Fatalf("ffmpeg exited (err=%v) before writing a playlist; see %s",
				job.Err(), filepath.Join(job.Dir(), "ffmpeg.log"))
		}
		if time.Now().After(deadline) {
			t.Fatalf("playlist never appeared within timeout; see %s",
				filepath.Join(job.Dir(), "ffmpeg.log"))
		}
		time.Sleep(100 * time.Millisecond)
	}

	data, err := os.ReadFile(job.PlaylistPath())
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("playlist file is empty")
	}
}
