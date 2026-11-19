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

// cleanupUserDataTask is a task whose work is a no-op, so a test can watch it
// fire without waiting for real work.
func cleanupUserDataTask(t *testing.T) taskDef {
	t.Helper()
	def, ok := taskByID("8240e309407dbbf43c99d30a6cbd239e")
	if !ok {
		t.Fatal("the cleanup user data task is not registered")
	}
	return def
}

// TestShutdownStopsTheScheduler: the task scheduler fires triggers until the
// server is shut down, and nothing fires after that. A server without Shutdown
// keeps its loops (watchdog, library monitor, scheduler) running for the life
// of the process, which for a test means writing under a temp directory that
// t.TempDir is already removing.
func TestShutdownStopsTheScheduler(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := startTestServer(t, st, Config{ServerName: "TestServer", DataDir: dir})
	def := cleanupUserDataTask(t)

	// The scheduler checks its triggers every 30 seconds by default; a short
	// step lets the test see a fire and then the absence of one.
	oldStep := schedulerStep
	schedulerStep = 50 * time.Millisecond
	t.Cleanup(func() { schedulerStep = oldStep })

	// One fire per second: fast enough to observe inside a test, slow enough
	// that a run is never mistaken for the next one.
	triggers := `[{"Type":"IntervalTrigger","IntervalTicks":10000000}]`
	if err := st.SetSetting(context.Background(), triggerSettingKey(def), triggers); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	lastRun := func() string {
		info := srv.taskInfo(def)
		if info.LastExecutionResult == nil {
			return ""
		}
		return info.LastExecutionResult.StartTimeUtc
	}
	deadline := time.Now().Add(20 * time.Second)
	for lastRun() == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	first := lastRun()
	if first == "" {
		t.Fatal("the interval trigger never fired")
	}

	srv.Shutdown()
	srv.Shutdown() // shutting down twice is harmless
	fired := lastRun()

	// Two more intervals: a scheduler that kept running would fire again.
	time.Sleep(2500 * time.Millisecond)
	if got := lastRun(); got != fired {
		t.Errorf("a trigger fired after Shutdown: %q became %q", fired, got)
	}
}

// TestShutdownStopsARunningBatch: a chapter batch in flight is stopped the way
// the stop button stops it, and Shutdown returns only once it has.
func TestShutdownStopsARunningBatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// A stand-in ffmpeg that takes half a second per picture: eight items
	// with one chapter each make a batch of about four seconds.
	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nsleep 0.5\nfor last; do :; done\nprintf x > \"$last\"\n"
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
	for i := 0; i < 8; i++ {
		item := storage.Item{
			ID: storage.ItemID(fmt.Sprintf("%032d", i)), Type: storage.ItemTypeMovie, LibraryID: "lib1",
			Name: fmt.Sprintf("Clip %d", i), Path: video,
			Chapters: []storage.ChapterRef{{StartTicks: 10_000_000}},
		}
		if err := st.UpsertItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	srv := startTestServer(t, st, Config{ServerName: "TestServer", DataDir: dir, TranscodeFFmpegPath: ffmpeg})
	srv.chapterimg.RunTask([]storage.Library{lib})
	deadline := time.Now().Add(10 * time.Second)
	for !srv.chapterimg.TaskRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.chapterimg.TaskRunning() {
		t.Fatal("the batch did not start")
	}

	srv.Shutdown()

	if srv.chapterimg.TaskRunning() {
		t.Error("the batch is still running after Shutdown returned")
	}
	if last := srv.chapterimg.TaskLast(); !last.Cancelled {
		t.Errorf("TaskLast() = %+v, want a cancelled batch", last)
	}
}
