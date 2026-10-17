// Package api implements the HTTP surface described in
// docs/04-api-compatibility.md, on top of the storage.Store interface
// (internal/storage). It is built entirely on stdlib net/http, using Go
// 1.22+ method+pattern routing on http.ServeMux — no third-party router,
// per AGENTS.md principle 2 ("single binary, zero required dependencies by
// default").
//
// Scope for this package right now: authentication (Authorization
// header parsing and token lookup) plus the System/Users endpoints needed to prove
// the auth flow end to end. Library/playback/session endpoints are later
// slices (see the package-level TODO markers, none — deliberately not
// stubbed here at all, to avoid dead routes).
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/chapterimg"
	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
	"github.com/rafaelvieiras/tinyfin/internal/trickplay"
)

// Version is the wire-visible server version reported in PublicSystemInfo
// and SystemInfo. It is the Jellyfin generation Tinyfin implements
// (wire.identity.R1: the reference 12.2 image reports 12.2.0), not a Tinyfin
// release number: jellyfin-web gates features on this value, so a lower one
// makes it offer the older feature set. Two constraints on its shape, both
// found by testing against a real jellyfin-web build:
//
//   - Must be a plain numeric-only semver (no "-dev"/pre-release suffix)
//     — the reference client SDK's compareVersions rejects anything else.
//   - Must be >= @jellyfin/sdk's compiled-in MINIMUM_VERSION, or the
//     client refuses to connect at all ("this server needs to be
//     updated").
const Version = "12.2.0"

// ProductName is the wire-visible product name (PublicSystemInfo.ProductName
// / SystemInfo.ProductName). This is NOT Tinyfin's own identity — it's a
// literal string real Jellyfin clients check against. Confirmed by testing
// against a real jellyfin-android build: jellyfin-sdk-kotlin's
// RecommendedServerDiscovery scores any candidate whose ProductName isn't
// exactly "Jellyfin Server" as BAD (see its PRODUCT_NAME constant), which
// silently sinks an otherwise-perfectly-good connection with no visible
// error beyond a generic "couldn't connect" — jellyfin-web and curl never
// hit this because they don't do this scoring pass at all. Tinyfin's own
// identity is reported instead via ServerName below.
const ProductName = "Jellyfin Server"

// DefaultServerName is Config.ServerName's default when left unset —
// Tinyfin's actual human-facing identity, independent of the wire-protocol
// ProductName constant above.
const DefaultServerName = "Tinyfin"

// Config carries the small set of server-identity knobs main assembles
// before constructing a Server. Everything here has a sensible default so
// a caller can pass a zero Config.
type Config struct {
	// ServerName is the human-facing name reported to clients
	// (PublicSystemInfo.ServerName / SystemInfo.ServerName). Defaults to
	// DefaultServerName.
	ServerName string

	// ServerID is the stable identifier reported as PublicSystemInfo.Id,
	// SystemInfo.Id, AuthenticationResult.ServerId, and UserDto.ServerId.
	// There is no SystemStore/server-identity table yet (only the stores
	// storage.Store composes today), so there is nowhere to persist one —
	// if left empty, NewServer mints a random one for the life of the
	// process. Known simplification: this value is not stable across
	// restarts unless a caller supplies one explicitly.
	ServerID string

	// TranscodeFFmpegPath is the jellyfin-ffmpeg binary path passed through
	// to internal/transcode.Config (docs/07's transcode worker role,
	// Phase 2). Empty means that package's own fallback: look up "ffmpeg"
	// on $PATH.
	TranscodeFFmpegPath string

	// TranscodeFontsDir is a directory of fonts libass uses to burn text
	// subtitles in (internal/transcode.Config.FontsDir). Empty means the
	// system fonts through fontconfig.
	TranscodeFontsDir string

	// TMDBAPIKey configures internal/scan's metadata.Provider (Phase 3,
	// docs/08 and docs/00-decision-log.md's D3) as a metadata.TMDBProvider.
	// Empty means metadata.NoopProvider{}: the scanner still catalogs
	// every item from filename-derived data alone (see that package's doc
	// comment), just without remote enrichment.
	TMDBAPIKey string

	// ScanFFprobePath is the ffprobe binary internal/scan uses to extract
	// technical media info while cataloging. Empty means "ffprobe" on
	// $PATH, matching TranscodeFFmpegPath's analogous fallback.
	ScanFFprobePath string

	// ScanImageDir is where internal/scan downloads and caches
	// metadata.Provider-returned artwork. Empty disables provider image
	// download (see scan.Config.ImageDir's doc comment) — items are still
	// fully cataloged, just without downloaded artwork beyond whatever a
	// library folder's own poster.jpg/fanart.jpg already provides.
	ScanImageDir string

	// ScanConcurrency bounds per-library scan workers (0 = scanner default).
	ScanConcurrency int
	// ScanProbeConcurrency bounds simultaneous ffprobe processes across all
	// scans (0 = ScanConcurrency).
	ScanProbeConcurrency int
	// ScanLibraryParallelism bounds how many libraries scan at once during
	// a refresh (0 = 3).
	ScanLibraryParallelism int
	// ScanForceProbe disables the incremental skip (re-probes every file).
	ScanForceProbe bool

	// TrickplayConcurrency bounds the ffmpeg processes generating trickplay
	// sheets at once (0 = 1; each runs single-threaded).
	TrickplayConcurrency int

	// DataDir is the real directory holding the SQLite database file and
	// its sidecar files (server-id, scan image cache) — cmd/tinyfin/main.go
	// passes filepath.Dir of its -data flag. Tinyfin has no separate
	// config/cache/log/metadata directory taxonomy the way the reference
	// server does (it keeps everything next to the database), so this one
	// path is the honest answer for every one of those concepts where a
	// caller (system_info_storage.go) needs to report on them. Empty is a
	// valid zero value only in tests that don't exercise that handler.
	DataDir string

	// WebDir is the resolved, existence-checked jellyfin-web directory
	// main.go serves at /web/ (empty/unset if no web UI is bundled — the
	// common non-WITH_WEB deployment, docs/00's D9). Kept here, rather than
	// re-stat'd independently, so system_info_storage.go's "is there a web
	// bundle to report on at all" question always agrees with what main.go
	// actually decided to serve.
	WebDir string

	// QuickConnectDisabled turns the QuickConnect sign-in flow off: the
	// zero value leaves it enabled, as the official clients expect.
	QuickConnectDisabled bool
}

// Server is the API/metadata role's HTTP surface (docs/03). The zero value
// is not usable — construct with NewServer.
type Server struct {
	// monitorKick wakes the real-time monitor's reconcile loop (kickMonitor).
	monitorKick chan struct{}

	store     storage.Store
	cfg       Config
	mux       *http.ServeMux
	transcode *transcode.Manager
	// hlsSessions maps "<item>|<PlaySessionId>" to the transcode job key it
	// is currently served from, so that a change of audio or subtitle
	// track in a session stops the job of the track it replaces.
	hlsMu       sync.Mutex
	hlsSessions map[string]string
	scanner     *scan.Scanner
	trickplay   *trickplay.Generator
	chapterimg  *chapterimg.Generator
	scanState   *scanTracker
	taskRTOnce  sync.Once
	taskRTv     *taskRuntime
	// scanLibSem bounds libraries scanning concurrently (runScan).
	scanLibSem chan struct{}
	sessions   *sessionRegistry
	hub        *wsHub
	quick      *quickConnectState
	// qcOverride is the runtime QuickConnect setting: 0 unset (the
	// command-line default applies), 1 enabled, 2 disabled.
	qcOverride atomic.Int32
	pinLimit   *pinThrottle
	// localNet is the runtime LocalNetworkSubnets setting (nil: unset).
	localNet atomic.Pointer[localNetwork]
	// remoteAccessOff is the network EnableRemoteAccess=false setting: a
	// caller outside the local network is answered 503.
	remoteAccessOff atomic.Bool
	// serverName is the runtime ServerName setting (startup wizard or
	// POST /System/Configuration); nil means cfg.ServerName applies.
	serverName atomic.Pointer[string]
	// wizardDone latches once setup is known complete, so the hot
	// StartupWizardCompleted reads never touch the store afterwards.
	wizardDone atomic.Bool
	// wizardMu serializes the wizard's state transitions (first account,
	// completion) so two concurrent posts cannot both win.
	wizardMu sync.Mutex
}

// NewServer builds a Server backed by store, applying cfg defaults where
// left zero. Call Handler to get the http.Handler to pass to
// http.ListenAndServe (or http.Server.Handler).
func NewServer(store storage.Store, cfg Config) *Server {
	if cfg.ServerName == "" {
		cfg.ServerName = DefaultServerName
	}
	if cfg.ServerID == "" {
		cfg.ServerID = randomID()
	} else if n, ok := guid.Normalize(cfg.ServerID); ok {
		cfg.ServerID = n
	}

	var provider metadata.Provider = metadata.NoopProvider{}
	if cfg.TMDBAPIKey != "" {
		provider = metadata.NewTMDBProvider(cfg.TMDBAPIKey)
	}

	s := &Server{
		store:     store,
		cfg:       cfg,
		mux:       http.NewServeMux(),
		transcode: transcode.New(transcode.Config{FFmpegPath: cfg.TranscodeFFmpegPath, FontsDir: cfg.TranscodeFontsDir}),
		scanner: scan.New(store, provider, scan.Config{
			FFprobePath:      cfg.ScanFFprobePath,
			ImageDir:         cfg.ScanImageDir,
			Concurrency:      cfg.ScanConcurrency,
			ProbeConcurrency: cfg.ScanProbeConcurrency,
			ForceProbe:       cfg.ScanForceProbe,
			DataDir:          cfg.DataDir,
		}),
		scanLibSem: make(chan struct{}, scanLibParallelism(cfg.ScanLibraryParallelism)),
		scanState:  &scanTracker{},
		sessions:   newSessionRegistry(),
		hub:        newWSHub(),
		quick:      newQuickConnectState(),
		pinLimit:   newPinThrottle(),
	}
	s.trickplay = trickplay.New(store, cfg.TranscodeFFmpegPath, cfg.DataDir, cfg.TrickplayConcurrency)
	s.chapterimg = chapterimg.New(store, cfg.TranscodeFFmpegPath, cfg.DataDir)
	s.loadRuntimeSettings()
	s.monitorKick = make(chan struct{}, 1)
	s.loadLocalNetwork()
	s.routes()
	go s.runWSWatchdog(context.Background())
	go s.runLibraryMonitor(context.Background())
	go s.runTaskScheduler(context.Background())
	return s
}

// scanTracker records the in-process state of the one background operation
// this project has today — a library scan (runScan) — so GET
// /ScheduledTasks (scheduled_tasks.go) can honestly report whether one is
// currently running and when one last finished, without a real
// task-scheduling engine behind it. runScan always executes on its own
// goroutine (library_admin.go, item_refresh.go), and more than one can run
// concurrently (POST /Library/Refresh fires one per library) — running
// counts concurrent scans rather than being a bool, so the "is anything
// running" answer stays correct until every one of them has finished.
// lastStart/lastEnd/lastErr describe only the single most recently
// started/finished scan, not a per-run history — a known simplification
// that matches this type's existing scope, not a new one.
type scanTracker struct {
	mu        sync.Mutex
	running   int
	lastStart time.Time
	lastEnd   time.Time
	lastErr   error
	// batchTotal/batchDone count the scans started/finished since the
	// tracker was last idle, the basis of the task's progress percentage.
	batchTotal, batchDone int
	cancelSeq             int
	cancels               map[int]context.CancelFunc
	lastCancelled         bool
}

// register stores cancel so cancelAll can stop the scan; the returned func
// unregisters it.
func (t *scanTracker) register(cancel context.CancelFunc) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancels == nil {
		t.cancels = map[int]context.CancelFunc{}
	}
	t.cancelSeq++
	id := t.cancelSeq
	t.cancels[id] = cancel
	return func() {
		t.mu.Lock()
		delete(t.cancels, id)
		t.mu.Unlock()
	}
}

// cancelAll cancels every in-flight scan and reports whether there was one.
func (t *scanTracker) cancelAll() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.cancels {
		c()
	}
	if len(t.cancels) > 0 {
		t.lastCancelled = true
	}
	return len(t.cancels) > 0
}

// progress returns the completed share of the current batch, in percent.
func (t *scanTracker) progress() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.batchTotal == 0 {
		return 0
	}
	return float64(t.batchDone) / float64(t.batchTotal) * 100
}

func (t *scanTracker) begin() {
	t.mu.Lock()
	if t.running == 0 {
		t.batchTotal, t.batchDone = 0, 0
		t.lastCancelled = false
	}
	t.batchTotal++
	t.running++
	t.lastStart = time.Now().UTC()
	t.mu.Unlock()
}

func (t *scanTracker) end(err error) {
	t.mu.Lock()
	t.running--
	t.batchDone++
	t.lastEnd = time.Now().UTC()
	t.lastErr = err
	t.mu.Unlock()
}

// snapshot reports whether a scan is currently in flight and the start/
// outcome of the most recently finished one (zero time.Time for both if
// none has finished yet this process's lifetime).
func (t *scanTracker) snapshot() (running bool, lastStart, lastEnd time.Time, lastErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running > 0, t.lastStart, t.lastEnd, t.lastErr
}

func scanLibParallelism(n int) int {
	if n <= 0 {
		return 3
	}
	return n
}

// runScan runs a full Scanner pass over lib in the background, logging its
// outcome — the same "outlives the triggering request" pattern
// hls.go/ensureTranscodeJob's doc comment documents for transcode jobs:
// library_admin.go's handlers return as soon as the scan is *queued*, not
// once it *completes*, matching how a real client's dashboard expects
// Library/Refresh and a refreshLibrary=true VirtualFolders call to behave
// (a background operation it can poll/observe progress on, once this
// project has a task-progress endpoint — not yet, a known gap).
func (s *Server) runScan(lib storage.Library) {
	s.runScanCtx(context.Background(), lib)
}

// runScanCtx is runScan with a caller-prepared context (scan.WithoutCollections).
func (s *Server) runScanCtx(ctx context.Context, lib storage.Library) {
	ctx, cancel := context.WithCancel(ctx)
	defer s.scanState.register(cancel)()
	defer cancel()
	s.scanState.begin()
	s.notifyScheduledTasks(ctx, true)
	// Bound how many libraries scan at once; the scan counts as running
	// while it waits for a slot.
	s.scanLibSem <- struct{}{}
	defer func() { <-s.scanLibSem }()
	stats, err := s.scanner.ScanLibrary(ctx, lib)
	s.scanState.end(err)
	s.notifyScheduledTasks(ctx, true)
	if err != nil {
		log.Printf("api: scanning library %q: %v", lib.Name, err)
		return
	}
	// Seek thumbnails follow the scan (and, when the library switched them
	// off, are removed). The options are read again: they may have been
	// saved while the scan was queued.
	if fresh, err := s.store.GetLibrary(ctx, lib.ID); err == nil {
		lib = fresh
	}
	s.trickplay.EnqueueLibrary(lib, forceTrickplay(ctx))
	s.chapterimg.EnqueueLibrary(lib)
	log.Printf("api: scanned library %q: %d items cataloged (%d probed, %d unchanged) in %s, %d enriched, %d warnings",
		lib.Name, stats.ItemsScanned, stats.ItemsProbed, stats.ItemsSkipped, stats.Duration.Round(time.Millisecond), stats.ItemsEnriched, len(stats.Errors))
}

// runItemRefresh re-enriches one catalog item against the configured
// metadata.Provider (scanner.RefreshItem) in the background.
//
// Deliberately does NOT touch scanState/notifyScheduledTasks the way runScan
// does: an early version of this function did, on the reasoning that
// item_refresh.go's single-item branch used to be an honest no-op and
// sharing scanState would at least make *something* show as running once it
// stopped being one. a reporter caught the real bug that introduced: GET
// /ScheduledTasks reports exactly one synthetic task, "Scan Media
// Libraries" (Key/Id "RefreshLibrary", scheduled_tasks.go), and
// jellyfin-web's Dashboard "Scan All Libraries" button spins/disables
// itself for as long as that one task's State isn't Idle — sharing
// scanState meant refreshing any single item's metadata made that button
// spin too, with no library scan running at all, a variant of the exact bug
// this session opened with. Confirmed live in jellyfin-web's own source: a
// single-item refresh never touches GET /ScheduledTasks or the
// ScheduledTasksInfo WebSocket message at all — it's tracked entirely
// separately, via BaseItemDto.RefreshProgress/RefreshStatus fields on the
// item itself plus a per-item RefreshProgress WebSocket message
// (react-client's emby-itemrefreshindicator/RefreshIndicator.tsx,
// subscribing by ItemId). Tinyfin doesn't implement that per-item
// mechanism yet — a documented gap (docs/11), not something this function
// should paper over by borrowing the library-scan tracker.
func (s *Server) runItemRefresh(itemID storage.ItemID, replaceImages bool) {
	// A folder (series, season, folder of a library) is re-walked first: what
	// is new on disk is cataloged and what is gone is forgotten, like a
	// library scan does for the part of the library it stands for.
	if present, err := s.scanner.RescanFolder(context.Background(), itemID); err != nil {
		log.Printf("api: rescanning folder %s: %v", itemID, err)
	} else if !present {
		return
	}
	if err := s.scanner.RefreshItem(context.Background(), itemID, replaceImages); err != nil {
		log.Printf("api: refreshing item %s: %v", itemID, err)
	}
}

// Handler returns the complete http.Handler for the server: case-insensitive
// path normalization (routing.go), then route dispatch wrapped in the
// auth-parsing middleware (see auth.go for why this is a single
// always-applied wrapper rather than N per-route wrappers), all wrapped in
// CORS handling (cors.go) so a browser-hosted client on another origin can
// reach any route, auth-preflight included, with access logging outermost
// so it sees the request exactly as the client sent it and the real final
// status code. The path shape rule (one trailing slash ignored, empty
// segments refused: wire.path.R1-R2) runs just inside CORS, before anything
// else looks at the path.
func (s *Server) Handler() http.Handler {
	return withAccessLog(withCORS(withTrailingSlash(withCanonicalIDs(s.withAuthContext(withCaseInsensitivePaths(s.mux))))))
}

// statusRecorder captures the status code a handler wrote, since
// http.ResponseWriter has no getter for it — withAccessLog needs the real
// value, not an assumption of 200, to be useful for diagnosing exactly
// which request a client-side "404"/"source error" corresponds to.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap exposes the underlying ResponseWriter to net/http's
// http.ResponseController — the standard-library-documented mechanism for
// a wrapper type to stay transparent to a capability check on the
// concrete writer it wraps (Hijack, Flush, ...). Without this,
// websocket.Accept (websocket.go) can't upgrade the connection at all: it
// only ever sees this wrapper, not the concrete *http.response underneath
// that actually implements http.Hijacker, and fails the upgrade with a 501
// — found by a real test (websocket_test.go) dialing /socket through the
// full middleware chain, not by inspection.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// withAccessLog logs method, path+query, status, and duration for every
// request. Added while debugging a real jellyfin-android client's video
// playback failing with "Source error" / HTTP 404: nothing in this
// project logged incoming requests at all, so there was no way to see
// what URL the native ExoPlayer data source actually requested (its own
// logcat trace doesn't include it) without this.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("tinyfin: %s %s %d %s", r.Method, r.URL.RequestURI(), rec.status, time.Since(start))
	})
}

// withTrailingSlash implements wire.path.R1-R2: a request path that ends in
// one slash is served exactly like the same path without it, for every
// route and method; a path with an empty segment (two slashes in a row,
// anywhere) is a plain 404. Without the second half http.ServeMux would
// answer such paths with a redirect to the cleaned form.
func withTrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.Contains(p, "//") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if len(p) > 1 && strings.HasSuffix(p, "/") {
			r2 := new(http.Request)
			*r2 = *r
			u := *r.URL
			u.Path = strings.TrimSuffix(p, "/")
			u.RawPath = ""
			r2.URL = &u
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

// withCaseInsensitivePaths rewrites r.URL.Path to a registered route's exact
// casing before it reaches the mux, if one matches case-insensitively — see
// normalizePathCase for why this is necessary.
func withCaseInsensitivePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = normalizePathCase(r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	// Public — no token required, either because it's the login route
	// itself or because the reference server exposes it unauthenticated.
	s.mux.HandleFunc("GET /System/Info/Public", s.handleSystemInfoPublic)
	s.mux.HandleFunc("POST /Users/AuthenticateByName", s.handleAuthenticateByName)
	// Registered as a literal path, not caught by GET /Users/{userId} below
	// — http.ServeMux (Go 1.22+) resolves the literal segment as more
	// specific than the wildcard regardless of registration order.
	s.mux.HandleFunc("GET /Users/Public", s.handleUsersPublic)
	s.mux.HandleFunc("GET /QuickConnect/Enabled", s.handleQuickConnectEnabled)
	s.mux.HandleFunc("POST /QuickConnect/Initiate", s.handleQuickConnectInitiate)
	s.mux.HandleFunc("GET /QuickConnect/Connect", s.handleQuickConnectConnect)
	s.mux.HandleFunc("POST /QuickConnect/Authorize", requireAuth(s.handleQuickConnectAuthorize))
	s.mux.HandleFunc("POST /Users/AuthenticateWithQuickConnect", s.handleAuthenticateWithQuickConnect)
	// Anonymous recovery flow: the PIN, read from the server, is the
	// credential.
	s.mux.HandleFunc("POST /Users/ForgotPassword", s.handleForgotPassword)
	s.mux.HandleFunc("POST /Users/ForgotPassword/Pin", s.handleForgotPasswordPin)
	s.mux.HandleFunc("GET /Branding/Configuration", s.handleBrandingConfiguration)

	// Everything else in this slice's scope requires a valid token.
	s.mux.HandleFunc("GET /System/Info", s.openUntilSetUp(s.handleSystemInfo))
	s.registerStartupRoutes()
	// Called once per session right after sign-in by every official client
	// (system.go's handleSystemEndpoint doc comment) — found 404ing by
	// testing a real jellyfin-web build via Playwright.
	s.mux.HandleFunc("GET /System/Endpoint", requireAuth(s.handleSystemEndpoint))
	s.mux.HandleFunc("GET /System/Info/Storage", requireAdmin(s.handleSystemInfoStorage))
	s.mux.HandleFunc("GET /ScheduledTasks", requireAdmin(s.handleScheduledTasks))
	// The Dashboard "Scan All Libraries" button's actual call (jellyfin-web
	// resolves the task by Key first — see scheduledTaskLibraryScanID's doc
	// comment in scheduled_tasks.go — then starts it by Id here). Cancel
	// (DELETE on this same path) and per-task GET/Triggers stay a documented
	// gap (docs/11) — no scan cancellation exists, and nothing today calls
	// them for this project's one synthetic task.
	s.mux.HandleFunc("DELETE /ScheduledTasks/Running/{taskId}", requireAdmin(s.handleScheduledTaskStop))
	s.mux.HandleFunc("GET /ScheduledTasks/{taskId}", requireAdmin(s.handleScheduledTaskGet))
	// "Running/{taskId}" and "{taskId}/Triggers" overlap for ServeMux, so
	// one pattern dispatches both.
	s.mux.HandleFunc("POST /ScheduledTasks/{first}/{second}", requireAdmin(s.handleScheduledTaskPost))
	s.mux.HandleFunc("GET /System/Logs", requireAdmin(s.handleSystemLogs))
	s.mux.HandleFunc("GET /System/Logs/Log", requireAdmin(s.handleSystemLogFile))
	s.mux.HandleFunc("GET /Backup", requireAdmin(s.handleBackupList))
	s.mux.HandleFunc("POST /Backup/Create", requireAdmin(s.handleBackupCreate))
	s.mux.HandleFunc("GET /Backup/Manifest", requireAdmin(s.handleBackupManifest))
	s.mux.HandleFunc("POST /Backup/Restore", requireAdmin(s.handleBackupRestore))
	s.mux.HandleFunc("GET /Plugins", requireAdmin(s.handleEmptyList))
	s.mux.HandleFunc("GET /Packages", requireAdmin(s.handleEmptyList))
	s.mux.HandleFunc("GET /Repositories", requireAdmin(s.handleEmptyList))
	s.mux.HandleFunc("GET /System/ActivityLog/Entries", requireAdmin(s.handleActivityLogEntries))
	// Dashboard overview page, called unconditionally on mount — see
	// handleConfigurationPages' doc comment (system.go).
	s.mux.HandleFunc("GET /web/ConfigurationPages", requireAuth(s.handleConfigurationPages))
	s.mux.HandleFunc("GET /Users/Me", requireAuth(s.handleUsersMe))
	// Create and delete are administrator-only (users.create, users.delete.R2);
	// list still only requires a valid token — a known simplification to be
	// tightened with the rest of the permission rules.
	s.mux.HandleFunc("GET /Users", requireAuth(s.handleUsersList))
	s.mux.HandleFunc("POST /Users/New", requireAdmin(s.handleUsersCreate))
	s.mux.HandleFunc("GET /Users/{userId}", requireAuth(s.handleUsersGet))
	s.mux.HandleFunc("DELETE /Users/{userId}", requireAdmin(s.handleUsersDelete))
	s.mux.HandleFunc("POST /Users/{userId}/Policy", requireAdmin(s.handleUsersPolicy))
	// Update and configuration: self or administrator (users.update,
	// users.configuration). The {userId} forms are the legacy aliases the
	// official web client still calls; the literal "New", "Configuration"
	// and "AuthenticateByName" paths win over the wildcard.
	s.mux.HandleFunc("POST /Users", requireAuth(s.handleUsersUpdate))
	s.mux.HandleFunc("POST /Users/{userId}", requireAuth(s.handleUsersUpdate))
	s.mux.HandleFunc("POST /Users/Configuration", requireAuth(s.handleUsersConfiguration))
	s.mux.HandleFunc("POST /Users/Password", requireAuth(s.handleUsersPassword))
	s.mux.HandleFunc("POST /Users/{userId}/Password", requireAuth(s.handleUsersPassword))
	s.mux.HandleFunc("POST /Users/{userId}/Configuration", requireAuth(s.handleUsersConfiguration))

	// Library and item navigation (library.go) — docs/04's highest-priority
	// group.
	s.mux.HandleFunc("GET /UserViews", requireAuth(s.handleUserViews))
	s.mux.HandleFunc("GET /Items", requireAuth(s.handleItemsList))
	// Same handler as GET /Items above, {userId} accepted but ignored —
	// exactly the precedent GET /Users/{userId}/Items/{itemId} below already
	// sets for the single-item form (both key off AuthFromContext(ctx).User.ID
	// for anything per-user, never the path segment). Needed because
	// jellyfin-web's Favorites screen (#/home?tab=1) calls this per-user
	// LISTING form, not GET /Items — found missing (plain 404, not a bad
	// response) by testing that screen against a real jellyfin-web build.
	s.mux.HandleFunc("GET /Users/{userId}/Items", requireAuth(s.handleItemsList))
	// Must precede GET /Items/{itemId} in registration only for the sake of
	// routing.go's routePaths (case-insensitive normalization needs the
	// literal checked before the wildcard would swallow it, same as
	// "/Users/Public" above) — http.ServeMux itself always resolves a
	// literal segment ahead of a wildcard regardless of order.
	s.mux.HandleFunc("GET /Items/Latest", requireAuth(s.handleItemsLatest))
	s.mux.HandleFunc("GET /Items/Root", requireAuth(s.handleItemsRoot))
	s.mux.HandleFunc("GET /Items/Filters", requireAuth(s.handleItemsFilters))
	s.mux.HandleFunc("GET /Items/Filters2", requireAuth(s.handleItemsFilters2))
	s.mux.HandleFunc("GET /Items/Counts", requireAuth(s.handleItemsCounts))
	s.mux.HandleFunc("GET /Items/{itemId}", requireAuth(s.handleItemGet))
	s.mux.HandleFunc("GET /Items/{itemId}/Ancestors", requireAuth(s.handleItemAncestors))
	s.mux.HandleFunc("GET /Items/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	// theme_media.go — found 404ing on every single item detail page load
	// by testing against a real jellyfin-web build (docs/11's route
	// coverage table, docs/12's "Movie detail page — Theme Media" section).
	s.mux.HandleFunc("GET /Items/{itemId}/ThemeMedia", requireAuth(s.handleThemeMedia))
	s.mux.HandleFunc("GET /Items/{itemId}/LocalTrailers", requireAuth(s.handleLocalTrailers))
	s.mux.HandleFunc("GET /Items/{itemId}/SpecialFeatures", requireAuth(s.handleSpecialFeatures))
	s.mux.HandleFunc("GET /Items/{itemId}/ThemeSongs", requireAuth(s.handleThemeSongs))
	s.mux.HandleFunc("GET /Items/{itemId}/ThemeVideos", requireAuth(s.handleThemeVideos))
	s.mux.HandleFunc("GET /Users/{userId}/Items/{itemId}/LocalTrailers", requireAuth(s.handleLocalTrailers))
	s.mux.HandleFunc("GET /Users/{userId}/Items/{itemId}/SpecialFeatures", requireAuth(s.handleSpecialFeatures))
	s.mux.HandleFunc("GET /Videos/{itemId}/AdditionalParts", requireAuth(s.handleAdditionalParts))
	s.mux.HandleFunc("GET /UserItems/Resume", requireAuth(s.handleUserItemsResume))
	s.mux.HandleFunc("GET /UserItems/{itemId}/UserData", requireAuth(s.handleUserItemDataGet))
	s.mux.HandleFunc("POST /UserItems/{itemId}/UserData", requireAuth(s.handleUserItemDataPost))
	s.mux.HandleFunc("GET /Shows/{seriesId}/Seasons", requireAuth(s.handleShowSeasons))
	s.mux.HandleFunc("GET /Shows/{seriesId}/Episodes", requireAuth(s.handleShowEpisodes))
	// Home screen's "next episode" row and the series detail page's
	// equivalent (nextup.go) — found 404ing by testing a real jellyfin-web
	// build via Playwright. No registration-order requirement:
	// "/Shows/NextUp" splits into 3 segments (counting the leading empty
	// one, same convention routing.go's routePaths uses), while both
	// routes above split into 4 — different segment counts mean
	// http.ServeMux (and routing.go's own normalizePathCase) can't confuse
	// this route with either regardless of registration order.
	s.mux.HandleFunc("GET /Shows/NextUp", requireAuth(s.handleShowsNextUp))
	s.mux.HandleFunc("GET /Studios", requireAuth(s.handleStudios))
	s.mux.HandleFunc("GET /Studios/{name}", requireAuth(s.handleStudioByName))
	s.mux.HandleFunc("GET /Artists", requireAuth(s.handleArtists("Artist")))
	s.mux.HandleFunc("GET /Artists/AlbumArtists", requireAuth(s.handleArtists("AlbumArtist")))
	s.mux.HandleFunc("GET /Artists/{name}", requireAuth(s.handleArtistByName))
	s.mux.HandleFunc("GET /Artists/InstantMix", requireAuth(s.handleInstantMixByIDs))
	s.mux.HandleFunc("GET /MusicGenres/InstantMix", requireAuth(s.handleInstantMixByIDs))
	s.mux.HandleFunc("GET /MusicGenres/{genreName}/InstantMix", requireAuth(s.handleInstantMixGenreName))
	s.mux.HandleFunc("GET /Items/{itemId}/InstantMix", requireAuth(s.handleInstantMixItem))
	s.mux.HandleFunc("GET /Songs/{itemId}/InstantMix", requireAuth(s.handleInstantMixItem))
	s.mux.HandleFunc("GET /Albums/{itemId}/InstantMix", requireAuth(s.handleInstantMixItem))
	s.mux.HandleFunc("GET /Artists/{itemId}/InstantMix", requireAuth(s.handleInstantMixItem))
	s.mux.HandleFunc("GET /Playlists/{itemId}/InstantMix", requireAuth(s.handleInstantMixItem))
	s.mux.HandleFunc("GET /MusicGenres", requireAuth(s.handleMusicGenres))
	s.mux.HandleFunc("GET /MusicGenres/{genreName}", requireAuth(s.handleMusicGenreByName))
	s.mux.HandleFunc("GET /Audio/{itemId}/Lyrics", requireAuth(s.handleAudioLyrics))
	s.mux.HandleFunc("GET /Audio/{itemId}/universal", requireAuth(s.handleAudioUniversal))
	s.mux.HandleFunc("GET /Audio/{itemId}/master.m3u8", requireAuth(s.handleAudioHLSMaster))
	s.mux.HandleFunc("GET /Audio/{itemId}/main.m3u8", requireAuth(s.handleAudioHLSPlaylist))
	s.mux.HandleFunc("GET /Genres", requireAuth(s.handleGenres))
	s.mux.HandleFunc("GET /Genres/{genreName}", requireAuth(s.handleGenreByName))
	s.mux.HandleFunc("GET /Persons", requireAuth(s.handlePersons))
	s.mux.HandleFunc("GET /Persons/{name}", requireAuth(s.handlePersonByName))
	s.mux.HandleFunc("GET /Years", requireAuth(s.handleYears))
	s.mux.HandleFunc("GET /Years/{year}", requireAuth(s.handleYearByNumber))
	s.mux.HandleFunc("GET /Search/Hints", requireAuth(s.handleSearchHints))
	s.mux.HandleFunc("GET /Movies/Recommendations", requireAuth(s.handleMovieRecommendations))
	s.mux.HandleFunc("GET /Items/Suggestions", requireAuth(s.handleItemsSuggestions))
	s.mux.HandleFunc("GET /Movies/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /Shows/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /Trailers/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /Artists/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /Albums/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /Items/{itemId}/Intros", requireAuth(s.handleItemIntros))
	s.mux.HandleFunc("GET /Users/{userId}/Items/{itemId}/Intros", requireAuth(s.handleItemIntros))
	s.mux.HandleFunc("GET /Items/{itemId}/Collections", requireAuth(s.handleItemCollections))
	// Playlists (playlists.go).
	s.mux.HandleFunc("POST /Playlists", requireAuth(s.handleCreatePlaylist))
	s.mux.HandleFunc("GET /Playlists/{playlistId}", requireAuth(s.handleGetPlaylist))
	s.mux.HandleFunc("POST /Playlists/{playlistId}", requireAuth(s.handleUpdatePlaylist))
	s.mux.HandleFunc("GET /Playlists/{playlistId}/Items", requireAuth(s.handlePlaylistItems))
	s.mux.HandleFunc("POST /Playlists/{playlistId}/Items", requireAuth(s.handleAddToPlaylist))
	s.mux.HandleFunc("DELETE /Playlists/{playlistId}/Items", requireAuth(s.handleRemoveFromPlaylist))
	s.mux.HandleFunc("POST /Playlists/{playlistId}/Items/{itemId}/Move/{newIndex}", requireAuth(s.handleMovePlaylistItem))
	s.mux.HandleFunc("GET /Playlists/{playlistId}/Users", requireAuth(s.handlePlaylistUsers))
	s.mux.HandleFunc("GET /Playlists/{playlistId}/Users/{userId}", requireAuth(s.handlePlaylistUser))
	s.mux.HandleFunc("POST /Playlists/{playlistId}/Users/{userId}", requireAuth(s.handleSetPlaylistUser))
	s.mux.HandleFunc("DELETE /Playlists/{playlistId}/Users/{userId}", requireAuth(s.handleDeletePlaylistUser))
	// Small utility routes (misc_routes.go).
	s.mux.HandleFunc("GET /System/Ping", s.handleSystemPing)
	s.mux.HandleFunc("POST /System/Ping", s.handleSystemPing)
	s.mux.HandleFunc("GET /GetUtcTime", s.handleGetUtcTime)
	s.mux.HandleFunc("GET /Localization/Options", s.openUntilSetUp(s.handleLocalizationOptions))
	s.mux.HandleFunc("GET /Localization/ParentalRatings", s.openUntilSetUp(s.handleLocalizationParentalRatings))
	// LiveTv/Programs — jellyfin-web's series detail page calls this
	// unconditionally even though no LiveTV tuner is ever configured here.
	// See library.go's handleLiveTvPrograms doc comment.
	s.mux.HandleFunc("GET /LiveTv/Programs", requireAuth(s.handleLiveTvPrograms))
	// Not in the current OpenAPI spec (docs/04's verification source), but
	// still sent by a real client: jellyfin-web's item-details page uses
	// the legacy jellyfin-apiclient package (same one behind the
	// lowercase Users/authenticatebyname call — see routing.go) to fetch
	// the item it's displaying, and that package calls this older
	// per-user form rather than plain GET /Items/{itemId}. Found by
	// testing against a real jellyfin-web build: left 404, it aborts the
	// whole details page instead of falling back. userId is accepted but
	// ignored, same as handleItemGet's plain form — no per-user
	// aggregate (PlayedPercentage, ...) exists yet to key off it.
	s.mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", requireAuth(s.handleItemGet))
	// Public, unlike every other route in this slice — see handleItemImage's
	// doc comment in library.go for why: a browser's plain <img src=...>
	// can't attach the Authorization header this project's other routes
	// require, and testing against a real jellyfin-web build confirmed the
	// image URLs it renders never carry a token or ApiKey query param
	// either, so requiring auth here makes every image 401 silently
	// (broken poster/backdrop, no console-visible crash).
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", s.handleItemImage)
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", s.handleItemImage)
	// Admin-only counterparts (item_images_admin.go) — list/upload/delete,
	// unlike the public byte-serving GET routes above.
	s.mux.HandleFunc("GET /Items/{itemId}/Images", requireAdmin(s.handleListItemImages))
	s.mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}", requireAdmin(s.handleUploadItemImage))
	s.mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}/{imageIndex}", requireAdmin(s.handleUploadItemImage))
	s.mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}", requireAdmin(s.handleDeleteItemImage))
	s.mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}/{imageIndex}", requireAdmin(s.handleDeleteItemImage))

	// Direct Play negotiation and streaming (playback.go). "GET" patterns
	// also match HEAD per net/http.ServeMux's documented behavior, so the
	// stream routes don't need a separate HEAD registration.
	s.mux.HandleFunc("GET /Items/{itemId}/PlaybackInfo", requireAuth(s.handlePlaybackInfo))
	s.mux.HandleFunc("POST /Items/{itemId}/PlaybackInfo", requireAuth(s.handlePlaybackInfo))
	// Must precede GET /Videos/{itemId}/{file} below in registration only
	// for the sake of routing.go's routePaths (case-insensitive
	// normalization needs the literal checked before the wildcard would
	// swallow it, same reasoning as "/Items/Latest" above) — http.ServeMux
	// itself always resolves a literal segment ahead of a wildcard
	// regardless of order. Remux/Direct Stream/Transcode (hls.go, docs/07's
	// ladder past Direct Play) — the transcode worker role, wired
	// in-process via internal/transcode.
	s.mux.HandleFunc("GET /Videos/{itemId}/Trickplay/{width}/tiles.m3u8", requireAuth(s.handleTrickplayTiles))
	s.mux.HandleFunc("GET /Videos/{itemId}/Trickplay/{width}/{file}", requireAuth(s.handleTrickplaySheet))
	s.mux.HandleFunc("GET /Videos/{itemId}/master.m3u8", requireAuth(s.handleHLSMaster))
	s.mux.HandleFunc("GET /Videos/{itemId}/hls1/{playlistId}/{segment}", requireAuth(s.handleHLSNumberedSegment))
	s.mux.HandleFunc("GET /Videos/{itemId}/main.m3u8", requireAuth(s.handleHLSPlaylist))
	// http.ServeMux wildcards must occupy a whole path segment — "stream.
	// {container}" mixes a literal suffix with a wildcard in one segment,
	// which ServeMux rejects at registration time. A single {file}
	// wildcard covers both /stream and /stream.{container}, and — since
	// hls.go — HLS segment files (seg_00000.ts) too; handleVideoStream
	// itself dispatches on the segment shape (see playback.go).
	s.mux.HandleFunc("GET /Items/{itemId}/Download", requireAuth(s.handleItemDownload))
	s.mux.HandleFunc("GET /Videos/{itemId}/{file}", requireAuth(s.handleVideoStream))
	s.mux.HandleFunc("GET /Audio/{itemId}/{file}", requireAuth(s.handleAudioStream))
	// Subtitles (subtitles.go). The file segment is "Stream.{format}" or
	// "subtitles.m3u8": ServeMux wildcards cover whole segments, so the
	// handler splits it itself. The reference serves the stream routes
	// anonymously; Tinyfin requires a token (divergence, specs/subtitles.md
	// subtitles.auth.R1) — the delivery URLs carry the ApiKey.
	s.mux.HandleFunc("GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/{file}", requireAuth(s.handleSubtitleStream))
	s.mux.HandleFunc("GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/{startTicks}/{file}", requireAuth(s.handleSubtitleStream))
	s.mux.HandleFunc("POST /Videos/{itemId}/Subtitles", requireSubtitleManagement(s.handleSubtitleUpload))
	s.mux.HandleFunc("DELETE /Videos/{itemId}/Subtitles/{index}", requireAdmin(s.handleSubtitleDelete))
	s.mux.HandleFunc("GET /Items/{itemId}/RemoteSearch/Subtitles/{language}", requireSubtitleManagement(s.handleRemoteSubtitleSearch))
	s.mux.HandleFunc("POST /Items/{itemId}/RemoteSearch/Subtitles/{subtitleId}", requireSubtitleManagement(s.handleRemoteSubtitleDownload))
	s.mux.HandleFunc("GET /Providers/Subtitles/Subtitles/{subtitleId}", requireSubtitleManagement(s.handleRemoteSubtitleGet))
	s.mux.HandleFunc("GET /FallbackFont/Fonts", requireAuth(s.handleFallbackFontList))
	s.mux.HandleFunc("GET /FallbackFont/Fonts/{name}", requireAuth(s.handleFallbackFont))
	// Bandwidth probe fired before a playback session starts
	// (playback.go's handlePlaybackBitrateTest doc comment) — found
	// 404ing by testing a real jellyfin-web build via Playwright.
	s.mux.HandleFunc("GET /Playback/BitrateTest", requireAuth(s.handlePlaybackBitrateTest))

	// Per-user, per-client UI settings (users.go's UserStore doc comment) —
	// requested by every jellyfin-web session immediately after login, and
	// left unhandled it fails hard enough to block the post-login redirect
	// entirely (found by testing against a real jellyfin-web build).
	s.mux.HandleFunc("GET /DisplayPreferences/{displayPreferencesId}", requireAuth(s.handleDisplayPreferencesGet))
	s.mux.HandleFunc("POST /DisplayPreferences/{displayPreferencesId}", requireAuth(s.handleDisplayPreferencesUpdate))

	// Session capability registration (system.go's handleSessionsCapabilitiesFull
	// doc comment) — called unconditionally by every official client right
	// after sign-in.
	s.mux.HandleFunc("POST /Sessions/Capabilities/Full", requireAuth(s.handleSessionsCapabilitiesFull))
	// users.token.R7-R8: ends the session of the token the request carries.
	s.mux.HandleFunc("POST /Sessions/Logout", requireAuth(s.handleSessionsLogout))
	// requireAdmin, not requireAuth: this lists every tracked session across
	// every user (IP address, device name, client) — the admin Dashboard
	// "Devices" widget this implements is the only confirmed caller, and
	// exposing that to any authenticated non-admin user would be a real
	// information-disclosure regression, not a scope simplification.
	s.mux.HandleFunc("GET /Sessions", requireAdmin(s.handleSessionsList))
	// Realtime session channel (websocket.go, docs/06, docs/12's "Dashboard
	// overview — the GET /socket realtime channel" section). requireAuth
	// rejects an unauthenticated upgrade attempt (401) before the socket is
	// ever accepted, matching docs/12's "auth reuses the same mechanism as
	// every other route" — including the ApiKey query-param fallback,
	// since withAuthContext resolves that unconditionally before this
	// handler runs.
	s.mux.HandleFunc("GET /socket", s.handleSocket)

	// API keys and runtime server configuration (administrator only).
	s.mux.HandleFunc("GET /Auth/Keys", requireAdmin(s.handleAuthKeysList))
	s.mux.HandleFunc("POST /Auth/Keys", requireAdmin(s.handleAuthKeysCreate))
	s.mux.HandleFunc("DELETE /Auth/Keys/{key}", requireAdmin(s.handleAuthKeysRevoke))
	s.mux.HandleFunc("GET /System/Configuration", requireAdmin(s.handleSystemConfigurationGet))
	s.mux.HandleFunc("POST /System/Configuration", requireAdmin(s.handleSystemConfigurationPost))
	s.mux.HandleFunc("GET /System/Configuration/network", requireAdmin(s.handleNetworkConfigurationGet))
	s.mux.HandleFunc("POST /System/Configuration/network", requireAdmin(s.handleNetworkConfigurationPost))

	// Playstate reporting and media segments (sessions_playback.go) — found
	// missing while mapping the whole native playback flow for coverage,
	// not by an individual crash report: the native player calls all four
	// of these around every playback, and their absence silently breaks
	// resume position/watched-state rather than visibly failing.
	s.mux.HandleFunc("POST /Sessions/Playing", requireAuth(s.handleSessionsPlaying))
	s.mux.HandleFunc("POST /Sessions/Playing/Progress", requireAuth(s.handleSessionsPlayingProgress))
	s.mux.HandleFunc("POST /Sessions/Playing/Stopped", requireAuth(s.handleSessionsPlayingStopped))
	s.mux.HandleFunc("POST /UserPlayedItems/{itemId}", requireAuth(s.handleUserPlayedItemsMark))
	// playstate.auth.R1: the remaining playstate routes answer 401 to an
	// anonymous caller too.
	s.mux.HandleFunc("DELETE /UserPlayedItems/{itemId}", requireAuth(s.handleUserPlayedItemsUnmark))
	s.mux.HandleFunc("POST /UserFavoriteItems/{itemId}", requireAuth(s.handleUserFavoriteItemAdd))
	s.mux.HandleFunc("DELETE /UserFavoriteItems/{itemId}", requireAuth(s.handleUserFavoriteItemRemove))
	s.mux.HandleFunc("POST /Sessions/Playing/Ping", requireAuth(s.handleSessionsPlayingPing))
	s.mux.HandleFunc("GET /MediaSegments/{itemId}", requireAuth(s.handleMediaSegments))

	// Legacy per-user favorite/watched routes (userdata.go) — the actual
	// paths jellyfin-web's bundled client calls for the item-detail page's
	// favorite/mark-watched buttons, distinct from the newer/SDK-shaped
	// POST /UserPlayedItems/{itemId} above. {userId} accepted, ignored —
	// same simplification as GET /Users/{userId}/Items/{itemId} above.
	s.mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", requireAuth(s.handleFavoriteItemAdd))
	s.mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", requireAuth(s.handleFavoriteItemRemove))
	s.mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", requireAuth(s.handleLegacyPlayedItemAdd))
	s.mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", requireAuth(s.handleLegacyPlayedItemRemove))

	// Library management (library_admin.go, Phase 3) — admin-only, unlike
	// every other route above: these create/remove libraries and trigger
	// scans, not just read the catalog.
	s.mux.HandleFunc("GET /Library/VirtualFolders", requireAdmin(s.handleGetVirtualFolders))
	s.mux.HandleFunc("POST /Library/VirtualFolders", requireAdmin(s.handleAddVirtualFolder))
	s.mux.HandleFunc("DELETE /Library/VirtualFolders", requireAdmin(s.handleRemoveVirtualFolder))
	s.mux.HandleFunc("POST /Library/VirtualFolders/LibraryOptions", requireAdmin(s.handleSaveLibraryOptions))
	s.mux.HandleFunc("POST /Library/VirtualFolders/Name", requireAdmin(s.handleRenameVirtualFolder))
	s.mux.HandleFunc("POST /Library/Refresh", requireAdmin(s.handleLibraryRefresh))

	// Collections (collections.go): management needs the user's
	// EnableCollectionManagement (or an administrator).
	s.mux.HandleFunc("POST /Collections", requireCollectionManagement(s.handleCreateCollection))
	s.mux.HandleFunc("POST /Collections/{collectionId}/Items", requireCollectionManagement(s.handleAddToCollection))
	s.mux.HandleFunc("DELETE /Collections/{collectionId}/Items", requireCollectionManagement(s.handleRemoveFromCollection))
	s.mux.HandleFunc("DELETE /Items/{itemId}", requireAuth(s.handleDeleteItem))
	// jellyfin-web's per-library "Rastrear biblioteca" menu action, distinct
	// from POST /Library/Refresh above (which scans every library) — see
	// item_refresh.go.
	s.mux.HandleFunc("POST /Items/{itemId}/Refresh", requireAdmin(s.handleItemRefresh))

	// Dashboard "Add/Manage Media Library" modal support (environment.go,
	// localization.go) — the folder browser and the language/country/
	// metadata-fetcher pickers the modal's "Library Settings" section is
	// gated on. Environment/* stays admin-only (filesystem browsing);
	// Localization/Libraries carry no library-management side effect, so
	// they're wired with requireAuth like any other "list of options" GET.
	s.mux.HandleFunc("GET /Environment/DefaultDirectoryBrowser", requireAdmin(s.handleDefaultDirectoryBrowser))
	s.mux.HandleFunc("GET /Environment/Drives", requireAdmin(s.handleEnvironmentDrives))
	s.mux.HandleFunc("GET /Environment/DirectoryContents", requireAdmin(s.handleDirectoryContents))
	s.mux.HandleFunc("GET /Environment/ParentPath", requireAdmin(s.handleParentPath))
	s.mux.HandleFunc("POST /Environment/ValidatePath", requireAdmin(s.handleValidatePath))
	s.mux.HandleFunc("GET /Localization/cultures", requireAuth(s.handleLocalizationCultures))
	s.mux.HandleFunc("GET /Localization/countries", requireAuth(s.handleLocalizationCountries))
	s.mux.HandleFunc("GET /Libraries/AvailableOptions", requireAuth(s.handleLibrariesAvailableOptions))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// randomID mints an opaque identifier in the canonical wire form: 32
// lowercase hex digits (internal/guid).
func randomID() string { return guid.New() }
