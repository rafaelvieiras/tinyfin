package transcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Job is one running (or finished) transcode job with its own output
// directory containing an HLS playlist and segment files, written
// progressively by the ffmpeg subprocess as it runs. A Job is created by
// Manager.Start and is safe for concurrent use — Done/Err are read from
// whatever goroutine polls the job's progress (e.g. an HTTP handler
// serving the playlist) while the exit state they report is written from
// the background goroutine Start spawns to wait on the subprocess.
type Job struct {
	sessionID   string
	startNumber int
	startTicks  int64
	dir         string
	cancel      context.CancelFunc

	mu      sync.Mutex
	done    bool
	exitErr error
}

// Dir returns the absolute path to the job's output directory. The HLS
// playlist (PlaylistPath) and every segment file ffmpeg writes live here;
// the API layer serves segments directly from this directory by name
// (e.g. Dir() + "/seg_00003.ts") rather than through a per-segment method,
// since the set of segment names isn't known up front and changes as
// ffmpeg keeps producing them.
func (j *Job) Dir() string {
	return j.dir
}

// PlaylistPath returns the absolute path to the job's HLS media playlist
// (main.m3u8 in Dir()) — the file the API layer polls for and then serves
// to the client once ffmpeg has produced it. This package writes a single
// media playlist per job, not a master playlist with variant streams:
// Manager already knows the one rendition a given Request describes (the
// playback decision picked it before handing off), so there's no
// alternative rendition for a master playlist to point at.
//
// The file may not exist yet when this is called — ffmpeg writes it only
// after encoding its first segment. Callers that need to wait for it
// should poll (os.Stat) rather than treating a missing file as a job
// failure; check Done/Err first to distinguish "still starting up" from
// "crashed before writing anything."
func (j *Job) PlaylistPath() string {
	return filepath.Join(j.dir, "main.m3u8")
}

// StartNumber is the number of the first segment the job writes.
func (j *Job) StartNumber() int { return j.startNumber }

// StartTicks is the position (100 ns ticks) the job started reading at.
func (j *Job) StartTicks() int64 { return j.startTicks }

// Written is the number of the last segment file the job has started
// writing (the one ffmpeg is on, maybe not yet complete), or -1 when none
// exists.
func (j *Job) Written(ext string) int {
	last := -1
	for n := j.startNumber; ; n++ {
		if _, err := os.Stat(filepath.Join(j.dir, fmt.Sprintf("seg_%05d.%s", n, ext))); err != nil {
			return last
		}
		last = n
	}
}

// SessionID returns the session ID this job was started under.
func (j *Job) SessionID() string {
	return j.sessionID
}

// Done reports whether the ffmpeg subprocess has exited (successfully or
// not). While Done is false, the job may still be starting up, actively
// encoding, or have been asked to stop but not yet torn down — callers
// waiting on output (e.g. for PlaylistPath to appear) should keep polling.
func (j *Job) Done() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

// Err returns the error the ffmpeg subprocess exited with, or nil if it
// hasn't exited yet or exited cleanly. A non-nil Err after Done is true
// means the job will never produce more output — a poller waiting for
// PlaylistPath (or a new segment) should give up and report failure
// instead of waiting for a file that will never arrive, per docs/07's
// requirement that this role tear itself down cleanly rather than hang.
func (j *Job) Err() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.exitErr
}

// setExited records the subprocess's exit outcome. Called exactly once,
// from the background goroutine Start spawns to wait on the subprocess.
func (j *Job) setExited(err error) {
	j.mu.Lock()
	j.done = true
	j.exitErr = err
	j.mu.Unlock()
}
