// Package transcode implements the transcode worker role described in
// docs/03-architecture.md ("Transcode worker role") and
// docs/07-transcoding-pipeline.md ("Execution"): given a Request built by
// internal/playback's decision logic (the Remux/Direct Stream/Transcode
// ladder — Direct Play never reaches this package, since it needs no
// server-side process at all), it builds a jellyfin-ffmpeg command line,
// runs it as a subprocess that writes an HLS playlist and segments into a
// per-job directory, and manages that subprocess's lifecycle: start, track
// running/exited state, and tear down on Stop or context cancellation.
//
// jellyfin-ffmpeg does 100% of the actual encode/decode work (docs/07,
// "Division of responsibility") — this package's own logic is limited to
// argument construction and process supervision; it never touches codec
// internals itself. It is deliberately stateless across jobs (docs/03:
// "nothing about a given transcode job depends on any other job's state"):
// Manager's job map is bookkeeping so a caller can look a running job up
// again by session ID, not shared state one job's execution depends on.
// That's also what keeps horizontal scaling of this role a deployment
// change rather than a code change (docs/03, decision D6) — nothing here
// assumes it's the only Manager instance in existence.
//
// This package works with plain paths and strings, not storage.Item or
// deviceprofile.DeviceProfile — turning a playback decision into a Request
// is the caller's job (internal/api, once it wires this package in), which
// keeps transcode usable standalone and unit-testable without either of
// those types.
package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// jobDirName is the fixed subdirectory name under Config.WorkDir (or
// os.TempDir()) that all job directories live under, so Tinyfin's own
// output is trivially distinguishable from unrelated files a shared temp
// directory might contain.
const jobDirName = "tinyfin-transcode"

// defaultSegmentSeconds is used when Request.SegmentSeconds is 0. 6 seconds
// is the same ballpark most HLS packagers (including jellyfin-ffmpeg's own
// defaults) use as a compromise between seek granularity and per-segment
// overhead.
const defaultSegmentSeconds = 6

// Config is process-wide transcode configuration, constructed once at
// server startup and passed to New.
type Config struct {
	// FFmpegPath is the path to the jellyfin-ffmpeg binary. Empty means
	// look up "ffmpeg" on $PATH (local/dev fallback) — exec.Command
	// resolves a bare name against $PATH itself, so no separate lookup is
	// needed here.
	FFmpegPath string
	// FFprobePath is the ffprobe binary the key frame scan uses. Empty means
	// the one next to FFmpegPath, else "ffprobe" on $PATH.
	FFprobePath string
	// WorkDir is the parent directory under which each job gets its own
	// subdirectory for HLS output (playlist + segments). Empty means
	// os.TempDir().
	WorkDir string
	// FontsDir is a directory of fonts for burned-in text subtitles
	// (libass' fontsdir). Empty means fontconfig's system fonts, which is
	// what a container with fonts installed (e.g. fonts-dejavu) wants.
	FontsDir string
}

// ffmpegPath returns the binary to invoke, applying the FFmpegPath-empty
// fallback described on Config.
func (c Config) ffmpegPath() string {
	if c.FFmpegPath != "" {
		return c.FFmpegPath
	}
	return "ffmpeg"
}

// baseDir returns the parent directory all job directories are created
// under, applying the WorkDir-empty fallback described on Config.
func (c Config) baseDir() string {
	dir := c.WorkDir
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, jobDirName)
}

// Request describes one Direct-Stream/Remux/Transcode job — the parameters
// internal/playback's decision logic (Phase 2 ladder, being extended
// separately) hands off, per docs/07's "Execution" section: source file
// reference, target mode, and the specific parameters that mode requires.
//
// A mode isn't its own field: it falls out of which of VideoCodec/
// AudioCodec are "copy" versus a real codec name (both copy → Remux; video
// copy only → Direct Stream; neither copy → Transcode), matching how
// buildArgs actually has to branch to build a correct command line anyway.
type Request struct {
	InputPath    string // source media file, read-only
	VideoCodec   string // target video codec, or "copy" for Direct Stream/Remux
	AudioCodec   string // target audio codec, or "copy" for Remux
	Container    string // target segment container, e.g. "ts"
	VideoBitrate int    // bits/sec, 0 = unset/unbounded
	AudioBitrate int    // bits/sec, 0 = unset/unbounded
	MaxHeight    int    // 0 = no scaling
	MaxWidth     int    // 0 = no width limit (the picture is scaled down to it)
	// H264Profile ("baseline", "constrainedbaseline", "main", "high") and
	// H264Level (the level times ten, 41 = 4.1) pin the encoder to what the
	// playlists announce; empty/0 keeps the default (high, 4.1).
	H264Profile    string
	H264Level      int
	StartTimeTicks int64 // seek offset, 100ns ticks (Jellyfin's tick unit), 0 = from start
	SegmentSeconds int   // HLS segment target duration, 0 = package default (6s)
	// StartNumber numbers the first segment the job writes (the segment the
	// request that started it asked for).
	StartNumber int
	// Cuts are the places (100 ns ticks of the source's own time) a copied
	// picture is cut at, the key frame plan of the file (see SegmentCuts).
	// nil: the HLS muxer cuts at the first key frame after SegmentSeconds.
	Cuts []int64

	// AudioStreamIndex is the index, within InputPath, of the audio stream
	// to map (ffmpeg's "0:N"); nil means the file's first audio stream.
	AudioStreamIndex *int
	// Subtitle, when set, is burned into the picture. It only takes effect
	// together with a re-encoded video (VideoCodec not "copy").
	Subtitle *Subtitle
	// FontsDir is filled from Config.FontsDir by Start.
	FontsDir string
	// SourceWidth and SourceHeight are the size of the source picture; Start
	// fills them for a picture subtitle burn-in, which needs the size of the
	// picture it overlays.
	SourceWidth, SourceHeight int
}

// Subtitle describes the track to burn in.
type Subtitle struct {
	// Path is the file the text is read from: the video itself for an
	// embedded track, the sidecar file for an external one (Embedded false).
	Path string
	// Embedded: the track is a stream of Path; Index is then its position
	// among the subtitle streams of that file (the subtitles filter's si=).
	Embedded bool
	Index    int
	// Bitmap marks picture subtitles (PGS, DVD, DVB), rendered with the
	// overlay filter from the stream StreamIndex of the input.
	Bitmap bool
	// StreamIndex is the stream of the input (of the sidecar file when
	// External) the pictures are read from.
	StreamIndex int
	// External: the pictures come from the sidecar file Path (idx, sup, mks),
	// a second input of the job. Filled by Start from what the file holds.
	External bool
	// Charset is the encoding of an external text file, for the charenc option
	// ("" leaves it out). Filled by Start.
	Charset string
	// Width and Height are the canvas size of a picture track, as ffprobe
	// reports it (0 when unknown). Filled by Start.
	Width, Height int
}

// Manager starts and tracks jobs, keyed by an opaque session ID the caller
// supplies (the API layer uses Jellyfin's PlaySessionId). The zero value is
// not usable; construct with New.
type Manager struct {
	cfg Config

	mu   sync.Mutex
	jobs map[string]*Job
	kf   kfCache
}

// New constructs a Manager from cfg. cfg is copied; later mutation of a cfg
// value passed by the caller has no effect.
func New(cfg Config) *Manager {
	return &Manager{
		cfg:  cfg,
		jobs: make(map[string]*Job),
	}
}

// Start launches a new jellyfin-ffmpeg subprocess for req under sessionID
// and returns the running Job. The subprocess runs under a context derived
// from ctx: canceling ctx (or a later call to Stop) kills it.
//
// If a job already exists for sessionID, Start stops it first and replaces
// it (see Stop) rather than returning the existing job or rejecting the
// call. A client re-issuing a playback request under the same
// PlaySessionId — a seek past the encoded window, a bitrate/profile
// renegotiation, a reconnect after a dropped connection — legitimately
// wants a fresh stream from new parameters, not the stale one; silently
// keeping the old subprocess running would leak both the process and its
// output directory once the caller stops referencing it.
func (m *Manager) Start(ctx context.Context, sessionID string, req Request) (*Job, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("transcode: empty session id")
	}
	// sessionID becomes a directory name component (jobDir); reject
	// anything that could escape m.cfg.baseDir() via path traversal or an
	// embedded separator, since it's caller-supplied (ultimately a value
	// off the wire, however indirectly) rather than something this
	// package generates itself.
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return nil, fmt.Errorf("transcode: invalid session id %q", sessionID)
	}

	if req.FontsDir == "" {
		req.FontsDir = m.cfg.FontsDir
	}

	// Stop+replace (see doc comment above). Safe even if no job exists yet
	// for this sessionID — Stop is a no-op in that case.
	m.Stop(sessionID)

	jobDir, err := filepath.Abs(filepath.Join(m.cfg.baseDir(), sessionID))
	if err != nil {
		return nil, fmt.Errorf("transcode: resolve job dir: %w", err)
	}
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, fmt.Errorf("transcode: create job dir: %w", err)
	}

	if req.Subtitle != nil {
		sub := *req.Subtitle
		req.Subtitle = &sub
		m.cfg.resolveBurn(&req, jobDir)
	}

	logFile, err := os.Create(filepath.Join(jobDir, "ffmpeg.log"))
	if err != nil {
		os.RemoveAll(jobDir)
		return nil, fmt.Errorf("transcode: create ffmpeg log: %w", err)
	}

	jobCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(jobCtx, m.cfg.ffmpegPath(), buildArgs(req)...)
	// Run ffmpeg with its working directory set to jobDir rather than
	// joining jobDir into each output path (see buildArgs's doc comment):
	// this is what makes the playlist it writes reference segments by
	// plain relative name instead of an absolute filesystem path.
	cmd.Dir = jobDir
	// ffmpeg logs everything (progress, warnings, errors) to stderr;
	// stdout is unused for this invocation. Both are redirected to a file
	// in the job dir rather than piped: a pipe nobody reads blocks the
	// subprocess once its OS buffer fills, and this package has no reason
	// to consume the output live — the file is there for diagnosability
	// (job crashed, wrong codec, bad input) if/when someone needs it.
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	job := &Job{
		startNumber: req.StartNumber,
		startTicks:  req.StartTimeTicks,
		sessionID:   sessionID,
		dir:         jobDir,
		cancel:      cancel,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		cancel()
		os.RemoveAll(jobDir)
		return nil, fmt.Errorf("transcode: start ffmpeg: %w", err)
	}

	m.mu.Lock()
	m.jobs[sessionID] = job
	m.mu.Unlock()

	// Run the wait in the background: Start must return the Job
	// immediately so the caller (the API layer) can begin polling for the
	// playlist file to appear, rather than blocking for however long the
	// whole transcode takes. The goroutine records the outcome on the Job
	// itself (mutex-protected — read from Get callers, written from here)
	// so a crashed job is observable instead of leaving a poller waiting
	// forever for a playlist file that will never be written.
	go func() {
		waitErr := cmd.Wait()
		logFile.Close()
		job.setExited(waitErr)
	}()

	return job, nil
}

// Get returns the running/finished job for sessionID, if any.
func (m *Manager) Get(sessionID string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[sessionID]
	return j, ok
}

// Stop kills the ffmpeg subprocess (if still running) for sessionID and
// removes its output directory. Safe to call more than once, and safe to
// call for a sessionID with no job (both are no-ops) — callers don't need
// to track whether a job exists before tearing it down (e.g. a session
// close handler that fires unconditionally).
func (m *Manager) Stop(sessionID string) {
	m.mu.Lock()
	job, ok := m.jobs[sessionID]
	if ok {
		delete(m.jobs, sessionID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}

	// Canceling the job's context runs exec.CommandContext's default
	// Cancel behavior (Process.Kill), which is immediate — this does not
	// wait for the subprocess to actually exit before removing the
	// directory below. That's deliberate: waiting would make Stop's
	// latency depend on how promptly a killed ffmpeg process tears down,
	// which is exactly the kind of per-job dependency docs/03 says this
	// role shouldn't have. Removing files a still-exiting process has open
	// is safe on the filesystems this project targets (Linux unlink
	// semantics); the background goroutine from Start still runs cmd.Wait
	// and closes the log file independently.
	job.cancel()
	os.RemoveAll(job.dir)
}
