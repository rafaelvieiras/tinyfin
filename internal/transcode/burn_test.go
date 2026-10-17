package transcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realFFmpeg returns an ffmpeg that can run the integration tests below
// (libx264, libmp3lame and the subtitles filter), or skips.
func realFFmpeg(t *testing.T) string {
	t.Helper()
	var cands []string
	for _, n := range []string{"jellyfin-ffmpeg", "ffmpeg"} {
		if p, err := exec.LookPath(n); err == nil {
			cands = append(cands, p)
		}
	}
	cands = append(cands, "/usr/lib/jellyfin-ffmpeg/ffmpeg", "/usr/bin/ffmpeg")
	for _, p := range cands {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		filters, _ := exec.Command(p, "-hide_banner", "-filters").Output()
		enc, _ := exec.Command(p, "-hide_banner", "-encoders").Output()
		if strings.Contains(string(filters), " subtitles ") && strings.Contains(string(enc), "libx264") && strings.Contains(string(enc), "libmp3lame") {
			return p
		}
	}
	t.Skip("no ffmpeg with libx264, libmp3lame and the subtitles filter")
	return ""
}

func probeFields(t *testing.T, ffmpeg, file, entries string) string {
	t.Helper()
	probe := filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
	if _, err := os.Stat(probe); err != nil {
		probe = "ffprobe"
	}
	out, err := exec.Command(probe, "-v", "error", "-show_entries", entries, "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Skipf("ffprobe unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// runArgs runs buildArgs(req) to completion in dir and returns the first
// segment's path.
func runArgs(t *testing.T, ffmpeg, dir string, req Request) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, buildArgs(req)...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg failed: %v\n%s", err, out)
	}
	return filepath.Join(dir, "seg_00000.ts")
}

func frameHash(t *testing.T, ffmpeg, seg, at string) string {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-v", "error", "-i", seg, "-ss", at, "-frames:v", "1", "-f", "md5", "-").Output()
	if err != nil {
		t.Fatalf("frame hash: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestAudioSelectionAndBurnIn_RealFFmpeg(t *testing.T) {
	ff := realFFmpeg(t)
	// the directory name carries a quote, a colon, parentheses and a space:
	// the characters that break an unescaped filter argument
	src := filepath.Join(t.TempDir(), "it's: (a) dir")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(src, "cue.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:03,500\nBURN IN TEXT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(src, "multi (1).mkv")
	gen := exec.Command(ff, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=4",
		"-i", srt, "-map", "0", "-map", "1", "-map", "2", "-map", "3",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a:0", "aac", "-c:a:1", "libmp3lame", "-c:s", "srt", "-shortest", in)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the input: %v\n%s", err, out)
	}
	base := Request{InputPath: in, VideoCodec: "h264", AudioCodec: "copy", Container: "ts", SegmentSeconds: 2}
	root := t.TempDir()

	t.Run("audio track is mapped", func(t *testing.T) {
		first := runArgs(t, ff, filepath.Join(root, "a0"), base)
		if got := probeFields(t, ff, first, "stream=codec_name,codec_type"); !strings.Contains(got, "aac,audio") {
			t.Fatalf("default track: %q, want aac", got)
		}
		two := 2
		r := base
		r.AudioStreamIndex = &two
		sec := runArgs(t, ff, filepath.Join(root, "a1"), r)
		if got := probeFields(t, ff, sec, "stream=codec_name,codec_type"); !strings.Contains(got, "mp3,audio") || strings.Contains(got, "aac") {
			t.Fatalf("second track: %q, want mp3 only", got)
		}
	})

	t.Run("text burn-in, embedded and external, with a seek", func(t *testing.T) {
		plain := runArgs(t, ff, filepath.Join(root, "p"), base)
		want := frameHash(t, ff, plain, "1")
		r := base
		r.Subtitle = &Subtitle{Path: in, Embedded: true, Index: 0}
		emb := runArgs(t, ff, filepath.Join(root, "e"), r)
		if got := frameHash(t, ff, emb, "1"); got == want {
			t.Fatal("embedded track was not burned in")
		}
		r.Subtitle = &Subtitle{Path: srt}
		ext := runArgs(t, ff, filepath.Join(root, "x"), r)
		if got := frameHash(t, ff, ext, "1"); got == want {
			t.Fatal("external file was not burned in")
		}
		// seek to 3 s: the cue (0.5-3.5 s) is on screen for the first half
		// second of the output only if the cue times follow the seek
		r.StartTimeTicks = 30_000_000
		seek := runArgs(t, ff, filepath.Join(root, "s"), r)
		noSub := base
		noSub.StartTimeTicks = 30_000_000
		seekPlain := runArgs(t, ff, filepath.Join(root, "sp"), noSub)
		if frameHash(t, ff, seek, "0.1") == frameHash(t, ff, seekPlain, "0.1") {
			t.Fatal("cue times do not follow the seek offset")
		}
	})
}

// bitmapSource builds a 4 s MKV whose subtitle track is a picture stream
// (codec "pgs" or "dvd") showing white block letters from 0.5 s to 3.5 s. The
// PGS stream comes from scripts/conformance-pgs.py (ffmpeg has no PGS
// encoder); the DVD track is an ffmpeg re-encode of it.
func bitmapSource(t *testing.T, ff, codec string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	sup := filepath.Join(dir, "a.sup")
	if out, err := exec.Command(py, "../../scripts/conformance-pgs.py", sup, "320", "240", "BURN IN", "0.5", "3.5").CombinedOutput(); err != nil {
		t.Skipf("cannot write the PGS stream: %v\n%s", err, out)
	}
	pgs := filepath.Join(dir, "pgs.mkv")
	gen := exec.Command(ff, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-i", sup,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-c:s", "copy", "-t", "4", pgs)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the input: %v\n%s", err, out)
	}
	if codec == "pgs" {
		return pgs
	}
	dvd := filepath.Join(dir, "dvd.mkv")
	conv := exec.Command(ff, "-v", "error", "-y", "-i", pgs, "-map", "0", "-c:v", "copy", "-c:a", "copy", "-c:s", "dvdsub", dvd)
	if out, err := conv.CombinedOutput(); err != nil {
		t.Skipf("cannot encode DVD subtitles: %v\n%s", err, out)
	}
	return dvd
}

// bandDiff counts the pixels of the bottom band (where the cue is drawn) that
// differ strongly between the frames of a and b at the given time.
func bandDiff(t *testing.T, ffmpeg, a, b, at string) int {
	t.Helper()
	raw := func(f string) []byte {
		out, err := exec.Command(ffmpeg, "-v", "error", "-i", f, "-ss", at, "-frames:v", "1",
			"-vf", "crop=320:50:0:190", "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("frame of %s at %s: %v", f, at, err)
		}
		return out
	}
	x, y := raw(a), raw(b)
	n := 0
	for i := range x {
		if i < len(y) && (int(x[i])-int(y[i]) > 60 || int(y[i])-int(x[i]) > 60) {
			n++
		}
	}
	return n
}

// TestBitmapBurnIn_RealFFmpeg checks the picture-subtitle burn-in end to end:
// the cue is on screen inside its window and gone after it (PGS clears the
// picture; a DVD cue that carries no end time stays).
func TestBitmapBurnIn_RealFFmpeg(t *testing.T) {
	ff := realFFmpeg(t)
	for _, codec := range []string{"pgs", "dvd"} {
		t.Run(codec, func(t *testing.T) {
			in := bitmapSource(t, ff, codec)
			base := Request{InputPath: in, VideoCodec: "h264", AudioCodec: "copy", Container: "ts", SegmentSeconds: 6}
			root := t.TempDir()
			plain := runArgs(t, ff, filepath.Join(root, "p"), base)
			r := base
			r.Subtitle = &Subtitle{Path: in, Bitmap: true, StreamIndex: 2}
			burnt := runArgs(t, ff, filepath.Join(root, "b"), r)
			if got := bandDiff(t, ff, plain, burnt, "0.1"); got > 50 {
				t.Errorf("%d pixels differ before the cue starts", got)
			}
			if got := bandDiff(t, ff, plain, burnt, "2"); got < 500 {
				t.Errorf("only %d pixels differ inside the cue: not burned in", got)
			}
			if codec == "pgs" {
				if got := bandDiff(t, ff, plain, burnt, "3.8"); got > 50 {
					t.Errorf("%d pixels differ after the PGS cue ended", got)
				}
			}
		})
	}
}
