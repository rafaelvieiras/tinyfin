package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeFFmpegBash writes a bash script standing in for the real
// jellyfin-ffmpeg binary, for lifecycle tests that exercise Manager's
// process-supervision logic (start/track/stop, exit-state recording)
// without depending on an actual ffmpeg install or actually transcoding
// anything. It ignores every flag buildArgs produces except the final
// positional argument (the playlist path, per buildArgs' contract), which
// it touches into existence immediately so PlaylistPath-polling logic has
// something to observe, then sleeps so tests can exercise Stop/context-
// cancellation killing a still-running job. Bash specifically (not POSIX
// sh) for "${!#}" last-argument indirection.
func writeFakeFFmpegBash(t *testing.T, dir, bashPath string, exitCode int, sleep time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ffmpeg.sh")
	sleepArg := "0"
	if sleep > 0 {
		sleepArg = sleep.String()
	}
	script := "#!" + bashPath + "\n" +
		"last=\"${!#}\"\n" +
		"touch \"$last\" 2>/dev/null\n" +
		"sleep " + sleepArg + "\n" +
		"exit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg script: %v", err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func testRequest() Request {
	return Request{
		InputPath:  "/dev/null",
		VideoCodec: "copy",
		AudioCodec: "copy",
		Container:  "ts",
	}
}

func TestManager_StartGetStop(t *testing.T) {
	// bash's "${!#}" indirection isn't POSIX sh; use bash explicitly if
	// present, otherwise skip this lifecycle test (it needs a real shell,
	// not ffmpeg).
	shPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on $PATH, skipping fake-ffmpeg lifecycle test")
	}

	work := t.TempDir()
	scriptDir := t.TempDir()
	script := writeFakeFFmpegBash(t, scriptDir, shPath, 0, 5*time.Second)

	m := New(Config{FFmpegPath: script, WorkDir: work})

	ctx := context.Background()
	job, err := m.Start(ctx, "sess-1", testRequest())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.SessionID() != "sess-1" {
		t.Fatalf("SessionID() = %q, want sess-1", job.SessionID())
	}

	got, ok := m.Get("sess-1")
	if !ok || got != job {
		t.Fatalf("Get(sess-1) = %v, %v; want the job Start returned", got, ok)
	}

	// The fake ffmpeg touches the playlist path immediately, so it should
	// appear quickly without needing to poll for long.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(job.PlaylistPath()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("playlist file %s did not appear in time", job.PlaylistPath())
		}
		time.Sleep(20 * time.Millisecond)
	}

	if job.Done() {
		t.Fatalf("job.Done() = true, want false (fake ffmpeg is still sleeping)")
	}

	m.Stop("sess-1")

	if _, ok := m.Get("sess-1"); ok {
		t.Fatalf("Get(sess-1) after Stop = found, want not found")
	}
	if _, err := os.Stat(job.Dir()); !os.IsNotExist(err) {
		t.Fatalf("job dir %s still exists after Stop", job.Dir())
	}

	// Safe to call twice.
	m.Stop("sess-1")

	// Safe to call for a session that never existed.
	m.Stop("never-existed")
}

func TestManager_StartReplacesExistingJob(t *testing.T) {
	shPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on $PATH, skipping fake-ffmpeg lifecycle test")
	}

	work := t.TempDir()
	scriptDir := t.TempDir()
	script := writeFakeFFmpegBash(t, scriptDir, shPath, 0, 5*time.Second)

	m := New(Config{FFmpegPath: script, WorkDir: work})
	ctx := context.Background()

	first, err := m.Start(ctx, "sess-2", testRequest())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	second, err := m.Start(ctx, "sess-2", testRequest())
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if second == first {
		t.Fatalf("Start did not replace the existing job for sess-2")
	}
	// Both jobs share a directory (keyed by sessionID alone), so the
	// replaced job's dir necessarily exists again once the second job
	// creates it — what matters is that the first job was actually
	// stopped (its subprocess killed), not that the path is gone.
	deadline := time.Now().Add(3 * time.Second)
	for !first.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("first job was not stopped (Done() never became true) after being replaced")
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, ok := m.Get("sess-2")
	if !ok || got != second {
		t.Fatalf("Get(sess-2) should return the replacement job")
	}

	m.Stop("sess-2")
}

func TestManager_JobRecordsNonZeroExit(t *testing.T) {
	shPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on $PATH, skipping fake-ffmpeg lifecycle test")
	}

	work := t.TempDir()
	scriptDir := t.TempDir()
	script := writeFakeFFmpegBash(t, scriptDir, shPath, 7, 0)

	m := New(Config{FFmpegPath: script, WorkDir: work})
	job, err := m.Start(context.Background(), "sess-3", testRequest())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop("sess-3")

	deadline := time.Now().Add(3 * time.Second)
	for !job.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("job did not report Done() in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Err() == nil {
		t.Fatalf("Err() = nil, want a non-nil error for a nonzero exit code")
	}
}

func TestManager_Start_InvalidSessionID(t *testing.T) {
	m := New(Config{WorkDir: t.TempDir()})
	for _, id := range []string{"", "../escape", "a/b", "."} {
		if _, err := m.Start(context.Background(), id, testRequest()); err == nil {
			t.Errorf("Start(sessionID=%q) = nil error, want an error", id)
		}
	}
}

func TestManager_Start_MissingFFmpegBinary(t *testing.T) {
	m := New(Config{FFmpegPath: filepath.Join(t.TempDir(), "does-not-exist"), WorkDir: t.TempDir()})
	job, err := m.Start(context.Background(), "sess-missing", testRequest())
	if err == nil {
		t.Fatalf("Start with a nonexistent ffmpeg binary = nil error, want error")
	}
	if job != nil {
		t.Fatalf("Start with a nonexistent ffmpeg binary returned a non-nil job")
	}
	// Job dir should not be left behind on a failed start.
	dir := filepath.Join(m.cfg.baseDir(), "sess-missing")
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("job dir %s left behind after failed Start", dir)
	}
}

func TestManager_Get_Unknown(t *testing.T) {
	m := New(Config{WorkDir: t.TempDir()})
	if _, ok := m.Get("nope"); ok {
		t.Fatalf("Get on unknown session id returned ok=true")
	}
}

func TestManager_ContextCancelKillsJob(t *testing.T) {
	shPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on $PATH, skipping fake-ffmpeg lifecycle test")
	}

	work := t.TempDir()
	scriptDir := t.TempDir()
	script := writeFakeFFmpegBash(t, scriptDir, shPath, 0, 30*time.Second)

	m := New(Config{FFmpegPath: script, WorkDir: work})
	ctx, cancel := context.WithCancel(context.Background())

	job, err := m.Start(ctx, "sess-4", testRequest())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()

	deadline := time.Now().Add(3 * time.Second)
	for !job.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("job did not report Done() after context cancellation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// exec's kill-on-cancel error doesn't reliably unwrap to
	// context.Canceled across platforms/Go versions, so this only asserts
	// that some error was recorded — the job was in fact killed rather
	// than exiting cleanly on its own (it was told to sleep 30s).
	if job.Err() == nil {
		t.Fatalf("Err() = nil after context cancellation, want a non-nil error")
	}

	m.Stop("sess-4")
}
