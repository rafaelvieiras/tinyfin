package chapterimg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// TestTaskProgressIsTheShareOfExaminedItems pins the reporting arithmetic:
// the percentage is examined/expected, it is capped, and it is only reported
// while a batch with a known size runs.
func TestTaskProgressIsTheShareOfExaminedItems(t *testing.T) {
	g := &Generator{taskJobs: 1, taskDone: 2, taskTotal: 8}
	if pct, ok := g.TaskProgress(); !ok || pct != 25 {
		t.Errorf("2 of 8 = %v, %v; want 25, true", pct, ok)
	}
	g.taskDone = 9
	if pct, _ := g.TaskProgress(); pct != 100 {
		t.Errorf("past the end = %v, want 100", pct)
	}
	g.taskDone, g.taskTotal = 0, 0
	if pct, ok := g.TaskProgress(); ok || pct != 0 {
		t.Errorf("empty selection = %v, %v; want 0, false", pct, ok)
	}
	g.taskTotal, g.taskJobs = 4, 0
	if _, ok := g.TaskProgress(); ok {
		t.Error("no batch running, but progress is reported")
	}
}

// slowFFmpeg is a stand-in for ffmpeg that takes its time and leaves a file
// where the picture was asked for: a batch long enough to observe, without
// generating a video to extract from.
func slowFFmpeg(t *testing.T, delay string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nsleep " + delay + "\nfor last; do :; done\nprintf x > \"$last\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunTaskReportsProgress runs a real batch and watches the percentage
// move: it is reported while the batch runs, it lands between the ends, and it
// stops being reported once the batch is done.
func TestRunTaskReportsProgress(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	lib := storage.Library{ID: "lib1", Name: "L", CollectionType: storage.CollectionTypeMovies,
		LibraryOptions: []byte(`{"EnableChapterImageExtraction":true}`)}
	if err := st.UpsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "v.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const items = 4
	for i := 0; i < items; i++ {
		item := storage.Item{
			ID: storage.ItemID(fmt.Sprintf("%032d", i)), Type: storage.ItemTypeMovie, LibraryID: "lib1",
			Name: fmt.Sprintf("Clip %d", i), Path: video,
			Chapters: []storage.ChapterRef{{StartTicks: 10_000_000}},
		}
		if err := st.UpsertItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	g := New(st, slowFFmpeg(t, "0.15"), dir)
	if _, ok := g.TaskProgress(); ok {
		t.Fatal("progress reported before the batch started")
	}
	g.RunTask([]storage.Library{lib})

	var seen []float64
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if pct, ok := g.TaskProgress(); ok {
			if len(seen) == 0 || seen[len(seen)-1] != pct {
				seen = append(seen, pct)
			}
		}
		if !g.TaskRunning() && len(seen) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	g.Wait()

	mid := false
	for _, pct := range seen {
		if pct > 0 && pct < 100 {
			mid = true
		}
	}
	if !mid || len(seen) < 2 {
		t.Errorf("progress went through %v; want it to advance in steps, including a value between 0 and 100", seen)
	}
	if _, ok := g.TaskProgress(); ok {
		t.Error("progress is still reported after the batch ended")
	}
	if r := g.TaskLast(); r.Cancelled || r.Start.IsZero() || r.End.Before(r.Start) {
		t.Errorf("last result = %+v; want a completed batch", r)
	}
}
