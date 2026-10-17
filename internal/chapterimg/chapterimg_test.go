package chapterimg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestParseLibraryOptions(t *testing.T) {
	if got := ParseLibraryOptions([]byte(`{"EnableChapterImageExtraction":true}`)); !got.Enable || got.DuringScan {
		t.Errorf("enable only: %+v", got)
	}
	if got := ParseLibraryOptions([]byte(`{"EnableChapterImageExtraction":true,"ExtractChapterImagesDuringLibraryScan":true}`)); !got.Enable || !got.DuringScan {
		t.Errorf("both: %+v", got)
	}
	if got := ParseLibraryOptions(nil); got.Enable || got.DuringScan {
		t.Errorf("none: %+v", got)
	}
	if got := ParseLibraryOptions([]byte(`{`)); got.Enable {
		t.Errorf("malformed: %+v", got)
	}
}

func TestExtractTicks(t *testing.T) {
	if got := ExtractTicks(storage.ChapterRef{StartTicks: 0}); got != 150_000_000 {
		t.Errorf("first chapter: %d", got)
	}
	if got := ExtractTicks(storage.ChapterRef{StartTicks: 60_000_000}); got != 60_000_000 {
		t.Errorf("later chapter: %d", got)
	}
}

// ramp makes a 640x360 video whose luma grows with time (8 per second), with
// chapters at the given starts (seconds).
func ramp(t *testing.T, dir string, secs int, starts ...int) string {
	t.Helper()
	meta := ";FFMETADATA1\n"
	for i, s := range starts {
		end := secs
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		meta += "[CHAPTER]\nTIMEBASE=1/1000\nSTART=" + strconv.Itoa(s*1000) + "\nEND=" + strconv.Itoa(end*1000) + "\n"
	}
	m := filepath.Join(dir, "m.txt")
	if err := os.WriteFile(m, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "v.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi",
		"-i", "color=c=black:s=640x360:r=5:d="+strconv.Itoa(secs)+",geq=lum='min(255,T*8)':cb=128:cr=128",
		"-i", m, "-map", "0", "-map_metadata", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the video: %v\n%s", err, b)
	}
	return out
}

// lumaSeconds reads the moment a still was taken from off its brightness.
func lumaSeconds(t *testing.T, jpg string) float64 {
	t.Helper()
	b, err := exec.Command("ffmpeg", "-v", "error", "-i", jpg, "-vf", "crop=20:20:300:170,format=gray", "-f", "rawvideo", "-").Output()
	if err != nil || len(b) == 0 {
		t.Fatalf("decode %s: %v", jpg, err)
	}
	sum := 0
	for _, v := range b {
		sum += int(v)
	}
	// limited-range luma of the ramp: about 9.3 per second, 18.6 below zero
	return (float64(sum)/float64(len(b)) + 18.6) / 9.32
}

func TestProcessTakesPicturesAtChapterStarts(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	vid := ramp(t, dir, 30, 0, 10, 20)
	g := New(nil, "ffmpeg", filepath.Join(dir, "data"))
	item := storage.Item{ID: "abc", Path: vid, Type: storage.ItemTypeMovie, Chapters: []storage.ChapterRef{
		{StartTicks: 0}, {StartTicks: 100_000_000}, {StartTicks: 200_000_000}}}
	g.process(context.Background(), item)
	want := []float64{15, 10, 20}
	for i, c := range item.Chapters {
		path, _, ok := g.Picture(item.ID, c)
		if !ok {
			t.Fatalf("chapter %d has no picture", i)
		}
		if got := lumaSeconds(t, path); got < want[i]-0.7 || got > want[i]+0.7 {
			t.Errorf("chapter %d picture taken at %.1f s, want %.0f", i, got, want[i])
		}
	}
	// unchanged: not made again
	path, mod, _ := g.Picture(item.ID, item.Chapters[1])
	g.process(context.Background(), item)
	if _, mod2, _ := g.Picture(item.ID, item.Chapters[1]); !mod2.Equal(mod) {
		t.Error("an unchanged item was processed again")
	}
	if Tag(path, mod) == "" || len(Tag(path, mod)) != 32 {
		t.Error("tag is not 32 hex digits")
	}
}

func TestProcessKeepsNothingWhenAPositionIsPastTheEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	vid := ramp(t, dir, 10, 0, 5)
	g := New(nil, "ffmpeg", filepath.Join(dir, "data"))
	item := storage.Item{ID: "short", Path: vid, Type: storage.ItemTypeMovie, Chapters: []storage.ChapterRef{{StartTicks: 0}, {StartTicks: 50_000_000}}}
	g.process(context.Background(), item)
	for i, c := range item.Chapters {
		if _, _, ok := g.Picture(item.ID, c); ok {
			t.Errorf("chapter %d has a picture", i)
		}
	}
}
