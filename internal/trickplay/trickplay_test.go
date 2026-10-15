package trickplay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

func TestPlanFor(t *testing.T) {
	cases := []struct {
		w, h      int
		ticks     int64
		wantW, wH int
		ok        bool
	}{
		{1920, 1080, 6000000000, 320, 180, true},
		{320, 240, 100, 320, 240, true},
		{160, 90, 100, 160, 90, true}, // never upscaled
		{1280, 545, 100, 320, 136, true},
		{0, 0, 100, 0, 0, false},
		{320, 240, 0, 0, 0, false},
	}
	for _, c := range cases {
		p, ok := PlanFor(c.w, c.h, c.ticks)
		if ok != c.ok || (ok && (p.Width != c.wantW || p.Height != c.wH || p.TileWidth != 10 || p.IntervalMS != 10000)) {
			t.Errorf("PlanFor(%d,%d) = %+v %v", c.w, c.h, p, ok)
		}
	}
}

func TestBandwidth(t *testing.T) {
	p, _ := PlanFor(320, 180, 1)
	// 754042 bytes over a 100 thumbnail grid of 10 s each: 6032.3 -> 6033
	if got := Bandwidth(p, 754042); got != 6033 {
		t.Errorf("Bandwidth = %d, want 6033", got)
	}
}

func TestParseLibraryOptions(t *testing.T) {
	if o := ParseLibraryOptions(nil); o != (LibraryOptions{}) {
		t.Errorf("nil = %+v", o)
	}
	o := ParseLibraryOptions([]byte(`{"EnableTrickplayImageExtraction":true,"SaveTrickplayWithMedia":true}`))
	if !o.Enable || o.DuringScan || !o.SaveWithMedia {
		t.Errorf("parsed = %+v", o)
	}
}

func TestGenerateIncrementalAndRemove(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	media := filepath.Join(dir, "media", "Clip (2020)")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(media, "Clip (2020).mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=duration=25:size=320x240:rate=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", file).CombinedOutput(); err != nil {
		t.Fatalf("making the clip: %v %s", err, out)
	}
	lib := storage.Library{ID: "lib1", Name: "L", CollectionType: storage.CollectionTypeMovies, Paths: []string{filepath.Dir(media)},
		LibraryOptions: []byte(`{"EnableTrickplayImageExtraction":true,"ExtractTrickplayImagesDuringLibraryScan":true}`)}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	w, h, ticks := 320, 240, int64(250000000)
	item := storage.Item{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: storage.ItemTypeMovie, LibraryID: "lib1", Name: "Clip", Path: file,
		RunTimeTicks: &ticks, MediaStreams: []storage.MediaStreamRef{{Type: storage.StreamTypeVideo, Width: &w, Height: &h}}}
	if err := st.UpsertItem(ctx, item); err != nil {
		t.Fatal(err)
	}

	g := New(st, ffmpeg, dir, 1)
	g.EnqueueLibrary(lib, false)
	g.Wait()
	infos, _ := st.QueryTrickplay(ctx, item.ID)
	if len(infos) != 1 {
		t.Fatalf("infos = %+v", infos)
	}
	in := infos[0]
	if in.Width != 320 || in.Height != 240 || in.TileWidth != 10 || in.Interval != 10000 || in.ThumbnailCount != 3 || in.Bandwidth < 1 {
		t.Errorf("info = %+v", in)
	}
	sheet := filepath.Join(g.DataRoot(), string(item.ID), "320", "0.jpg")
	st1, err := os.Stat(sheet)
	if err != nil {
		t.Fatalf("sheet missing: %v", err)
	}
	if got := g.DirFor(item, in); got != filepath.Dir(sheet) {
		t.Errorf("DirFor = %q", got)
	}

	// Unchanged media: not made again.
	os.Chtimes(sheet, st1.ModTime().Add(-3600e9), st1.ModTime().Add(-3600e9))
	before, _ := os.Stat(sheet)
	g.EnqueueLibrary(lib, false)
	g.Wait()
	after, _ := os.Stat(sheet)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged video was processed again")
	}
	// Forced: made again.
	g.EnqueueItem(lib, item, true)
	g.Wait()
	if forced, _ := os.Stat(sheet); forced.ModTime().Equal(before.ModTime()) {
		t.Error("force did not regenerate")
	}

	// Option off: everything goes.
	lib.LibraryOptions = nil
	g.EnqueueLibrary(lib, false)
	g.Wait()
	if infos, _ := st.QueryTrickplay(ctx, item.ID); len(infos) != 0 {
		t.Errorf("infos after disabling = %+v", infos)
	}
	if _, err := os.Stat(sheet); err == nil {
		t.Error("sheet still there after disabling")
	}
}
