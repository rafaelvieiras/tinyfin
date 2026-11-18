package api

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

// chapterImagesTask is the "Extract Chapter Images" task: the one whose
// default trigger carries a four hour MaxRuntimeTicks.
func chapterImagesTask(t *testing.T) taskDef {
	t.Helper()
	def, ok := taskByID("4e6637c832ed644d1af3370a2506e80a")
	if !ok {
		t.Fatal("the chapter image task is not registered")
	}
	return def
}

// slowVideoStore is a store with one library that extracts chapter pictures
// and four items to extract them for, plus a stand-in ffmpeg that takes delay
// per picture. Together they make a batch of known, controllable length
// without generating media.
type slowFixture struct {
	srv    *Server
	def    taskDef
	ffmpeg string
	video  string
}

func slowVideoStore(t *testing.T, delay string, items int) *slowFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nsleep " + delay + "\nfor last; do :; done\nprintf x > \"$last\"\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

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

	srv := NewServer(st, Config{ServerName: "TestServer", DataDir: dir, TranscodeFFmpegPath: ffmpeg})
	return &slowFixture{srv: srv, def: chapterImagesTask(t), ffmpeg: ffmpeg, video: video}
}

// waitForIdle returns the task info once it stopped running.
func waitForIdle(t *testing.T, srv *Server, def taskDef, within time.Duration) scheduledTaskInfo {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if info := srv.taskInfo(def); info.State == "Idle" {
			return info
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the task was still running after %s", within)
	return scheduledTaskInfo{}
}

// TestMaxRuntimeTicksCancelsTheRun is admin.tasks.R13: a run started by a
// trigger that carries MaxRuntimeTicks is stopped once it passes the limit,
// and it reports Cancelled — the same end a manual stop gives it. The batch it
// runs here would take about four seconds on its own; the limit is two.
func TestMaxRuntimeTicksCancelsTheRun(t *testing.T) {
	f := slowVideoStore(t, "0.5", 8)
	srv, def := f.srv, f.def
	limit := int64(20_000_000) // two seconds, half of the eight item batch
	if err := srv.startTask(context.Background(), def, &limit); err != nil {
		t.Fatalf("startTask: %v", err)
	}

	// While it runs, the readout is a real percentage, not a frozen zero.
	sawProgress := false
	for i := 0; i < 300 && !sawProgress; i++ {
		info := srv.taskInfo(def)
		if info.State != "Running" {
			break
		}
		if info.CurrentProgressPercentage != nil && *info.CurrentProgressPercentage > 0 {
			sawProgress = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawProgress {
		t.Error("the running task never reported a percentage above zero")
	}

	info := waitForIdle(t, srv, def, 30*time.Second)
	if info.LastExecutionResult == nil {
		t.Fatal("no last execution result")
	}
	if got := info.LastExecutionResult.Status; got != "Cancelled" {
		t.Errorf("Status = %q, want Cancelled", got)
	}
	if info.CurrentProgressPercentage != nil {
		t.Error("a task that is not running reports a percentage")
	}
}

// TestHandStartedRunIsNotBounded: MaxRuntimeTicks belongs to the trigger that
// fired the run, so a run a client started itself runs to the end.
func TestHandStartedRunIsNotBounded(t *testing.T) {
	f := slowVideoStore(t, "0.05", 4)
	srv, def := f.srv, f.def
	if err := srv.startTask(context.Background(), def, nil); err != nil {
		t.Fatalf("startTask: %v", err)
	}
	info := waitForIdle(t, srv, def, 30*time.Second)
	if info.LastExecutionResult == nil || info.LastExecutionResult.Status != "Completed" {
		t.Errorf("last execution result = %+v, want Completed", info.LastExecutionResult)
	}
}

// TestDueTriggerCarriesTheRuntimeLimit: the scheduler needs the trigger that
// fired, not just the fact that one did — MaxRuntimeTicks lives on the trigger.
func TestDueTriggerCarriesTheRuntimeLimit(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := NewServer(st, Config{ServerName: "TestServer", DataDir: dir})
	def := chapterImagesTask(t)

	now := time.Now()
	at := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	tr := srv.dueTrigger(def, now, at.Add(-time.Second), at.Add(time.Second), false)
	if tr == nil {
		t.Fatal("the 02:00 daily trigger did not fire")
	}
	if tr.MaxRuntimeTicks == nil || *tr.MaxRuntimeTicks != 144000000000 {
		t.Errorf("MaxRuntimeTicks = %v, want the four hour default", tr.MaxRuntimeTicks)
	}
	if tr = srv.dueTrigger(def, now, at.Add(2*time.Hour), at.Add(2*time.Hour+time.Second), false); tr != nil {
		t.Errorf("the trigger fired at %v, outside its time of day", tr.Type)
	}
}

// setFFmpegDelay rewrites the stand-in so the next run takes a different time.
func setFFmpegDelay(t *testing.T, path, delay string) {
	t.Helper()
	script := "#!/bin/sh\nsleep " + delay + "\nfor last; do :; done\nprintf x > \"$last\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestAStaleRuntimeLimitDoesNotStopTheNextRun: the timer belongs to the run
// that armed it. A short run that finished long before its limit, followed by a
// run the client started itself, must not be cancelled when the first run's
// timer goes off.
func TestAStaleRuntimeLimitDoesNotStopTheNextRun(t *testing.T) {
	f := slowVideoStore(t, "0.05", 8) // 0.4 s of batch
	srv, def := f.srv, f.def
	limit := int64(10_000_000) // one second: the timer goes off during the next run
	if err := srv.startTask(context.Background(), def, &limit); err != nil {
		t.Fatalf("startTask: %v", err)
	}
	if info := waitForIdle(t, srv, def, 30*time.Second); info.LastExecutionResult == nil ||
		info.LastExecutionResult.Status != "Completed" {
		t.Fatalf("the short run = %+v, want Completed", info.LastExecutionResult)
	}

	// The next run has to do the work again: the batch skips items whose file
	// is unchanged, so the media file gets a new size and date.
	if err := os.WriteFile(f.video, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	setFFmpegDelay(t, f.ffmpeg, "0.5") // and it takes four seconds
	if err := srv.startTask(context.Background(), def, nil); err != nil {
		t.Fatalf("startTask: %v", err)
	}
	info := waitForIdle(t, srv, def, 30*time.Second)
	if info.LastExecutionResult == nil || info.LastExecutionResult.Status != "Completed" {
		t.Errorf("the follow-up run = %+v, want Completed (the old timer must not stop it)", info.LastExecutionResult)
	}
}
