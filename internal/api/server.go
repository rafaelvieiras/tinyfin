// Package api implements the HTTP surface described in
// docs/04-api-compatibility.md, on top of the storage.Store interface
// (internal/storage). It is built entirely on stdlib net/http, using Go
// 1.22+ method+pattern routing on http.ServeMux — no third-party router,
// per AGENTS.md principle 2 ("single binary, zero required dependencies by
// default").
//
// Scope for this package right now: authentication (X-Emby-Authorization
// parsing and token lookup) plus the System/Users endpoints needed to prove
// the auth flow end to end. Library/playback/session endpoints are later
// slices (see the package-level TODO markers, none — deliberately not
// stubbed here at all, to avoid dead routes).
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/metadata"
	"github.com/rafaelvieiras/tinyfin/internal/scan"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/transcode"
)

// Version is the wire-visible server version reported in PublicSystemInfo
// and SystemInfo. Tinyfin has no release-tagging/version-tracking system
// yet (that's outside this slice's scope; see open item O1) so this is a
// hardcoded placeholder until one exists. Two constraints on its value,
// both found by testing against a real jellyfin-web build rather than
// something docs/04 could have predicted from the OpenAPI schema alone:
//
//   - Must be a plain numeric-only semver (no "-dev"/pre-release suffix)
//     — the reference client SDK's compareVersions rejects anything else.
//   - Must be >= @jellyfin/sdk's compiled-in MINIMUM_VERSION, or the
//     client refuses to connect at all ("this server needs to be
//     updated"). That constant is 10.10.0 as of the jellyfin-web build
//     this was verified against; report the same value here as the
//     compatibility baseline this project targets — the same number O1
//     still needs to pin formally, this is not a substitute for that
//     decision, just what unblocks manual testing today.
const Version = "10.10.0"

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
}

// Server is the API/metadata role's HTTP surface (docs/03). The zero value
// is not usable — construct with NewServer.
type Server struct {
	store     storage.Store
	cfg       Config
	mux       *http.ServeMux
	transcode *transcode.Manager
	scanner   *scan.Scanner
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
	}

	var provider metadata.Provider = metadata.NoopProvider{}
	if cfg.TMDBAPIKey != "" {
		provider = metadata.NewTMDBProvider(cfg.TMDBAPIKey)
	}

	s := &Server{
		store:     store,
		cfg:       cfg,
		mux:       http.NewServeMux(),
		transcode: transcode.New(transcode.Config{FFmpegPath: cfg.TranscodeFFmpegPath}),
		scanner: scan.New(store, provider, scan.Config{
			FFprobePath: cfg.ScanFFprobePath,
			ImageDir:    cfg.ScanImageDir,
		}),
	}
	s.routes()
	return s
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
	stats, err := s.scanner.ScanLibrary(context.Background(), lib)
	if err != nil {
		log.Printf("api: scanning library %q: %v", lib.Name, err)
		return
	}
	log.Printf("api: scanned library %q: %d items cataloged, %d enriched, %d warnings",
		lib.Name, stats.ItemsScanned, stats.ItemsEnriched, len(stats.Errors))
}

// Handler returns the complete http.Handler for the server: case-insensitive
// path normalization (routing.go), then route dispatch wrapped in the
// auth-parsing middleware (see auth.go for why this is a single
// always-applied wrapper rather than N per-route wrappers), all wrapped in
// CORS handling (cors.go) so a browser-hosted client on another origin can
// reach any route, auth-preflight included, with access logging outermost
// so it sees the request exactly as the client sent it and the real final
// status code.
func (s *Server) Handler() http.Handler {
	return withAccessLog(withCORS(s.withAuthContext(withCaseInsensitivePaths(s.mux))))
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
	s.mux.HandleFunc("GET /Branding/Configuration", s.handleBrandingConfiguration)

	// Everything else in this slice's scope requires a valid token.
	s.mux.HandleFunc("GET /System/Info", requireAuth(s.handleSystemInfo))
	// Called once per session right after sign-in by every official client
	// (system.go's handleSystemEndpoint doc comment) — found 404ing by
	// testing a real jellyfin-web build via Playwright.
	s.mux.HandleFunc("GET /System/Endpoint", requireAuth(s.handleSystemEndpoint))
	s.mux.HandleFunc("GET /Users/Me", requireAuth(s.handleUsersMe))
	// Admin-only in the reference server (UserController's [Authorize]
	// policy requires elevation for the list/create/delete routes below).
	// Tinyfin has no permissions system yet, so this only checks for a
	// valid token — anyone logged in can list/create/delete users right
	// now. Known simplification, not a silent gap: tighten this once a
	// permissions model exists.
	s.mux.HandleFunc("GET /Users", requireAuth(s.handleUsersList))
	s.mux.HandleFunc("POST /Users/New", requireAuth(s.handleUsersCreate))
	s.mux.HandleFunc("GET /Users/{userId}", requireAuth(s.handleUsersGet))
	s.mux.HandleFunc("DELETE /Users/{userId}", requireAuth(s.handleUsersDelete))

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
	s.mux.HandleFunc("GET /Items/Filters", requireAuth(s.handleItemsFilters))
	s.mux.HandleFunc("GET /Items/Filters2", requireAuth(s.handleItemsFilters2))
	s.mux.HandleFunc("GET /Items/{itemId}", requireAuth(s.handleItemGet))
	s.mux.HandleFunc("GET /Items/{itemId}/Ancestors", requireAuth(s.handleItemAncestors))
	s.mux.HandleFunc("GET /Items/{itemId}/Similar", requireAuth(s.handleItemSimilar))
	s.mux.HandleFunc("GET /UserItems/Resume", requireAuth(s.handleUserItemsResume))
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
	// can't attach the X-Emby-Token header this project's other routes
	// require, and testing against a real jellyfin-web build confirmed the
	// image URLs it renders never carry a token or api_key query param
	// either, so requiring auth here makes every image 401 silently
	// (broken poster/backdrop, no console-visible crash).
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", s.handleItemImage)
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", s.handleItemImage)

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
	s.mux.HandleFunc("GET /Videos/{itemId}/master.m3u8", requireAuth(s.handleHLSMaster))
	s.mux.HandleFunc("GET /Videos/{itemId}/main.m3u8", requireAuth(s.handleHLSPlaylist))
	// http.ServeMux wildcards must occupy a whole path segment — "stream.
	// {container}" mixes a literal suffix with a wildcard in one segment,
	// which ServeMux rejects at registration time. A single {file}
	// wildcard covers both /stream and /stream.{container}, and — since
	// hls.go — HLS segment files (seg_00000.ts) too; handleVideoStream
	// itself dispatches on the segment shape (see playback.go).
	s.mux.HandleFunc("GET /Videos/{itemId}/{file}", requireAuth(s.handleVideoStream))
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

	// Playstate reporting and media segments (sessions_playback.go) — found
	// missing while mapping the whole native playback flow for coverage,
	// not by an individual crash report: the native player calls all four
	// of these around every playback, and their absence silently breaks
	// resume position/watched-state rather than visibly failing.
	s.mux.HandleFunc("POST /Sessions/Playing", requireAuth(s.handleSessionsPlaying))
	s.mux.HandleFunc("POST /Sessions/Playing/Progress", requireAuth(s.handleSessionsPlayingProgress))
	s.mux.HandleFunc("POST /Sessions/Playing/Stopped", requireAuth(s.handleSessionsPlayingStopped))
	s.mux.HandleFunc("POST /UserPlayedItems/{itemId}", requireAuth(s.handleUserPlayedItemsMark))
	s.mux.HandleFunc("GET /MediaSegments/{itemId}", requireAuth(s.handleMediaSegments))

	// Legacy per-user favorite/watched routes (userdata.go) — the actual
	// paths jellyfin-web's bundled client calls for the item-detail page's
	// favorite/mark-watched buttons, distinct from the newer/SDK-shaped
	// POST /UserPlayedItems/{itemId} above. {userId} accepted, ignored —
	// same simplification as GET /Users/{userId}/Items/{itemId} above.
	s.mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", requireAuth(s.handleFavoriteItemAdd))
	s.mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", requireAuth(s.handleFavoriteItemRemove))
	s.mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", requireAuth(s.handlePlayedItemAdd))
	s.mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", requireAuth(s.handlePlayedItemRemove))

	// Library management (library_admin.go, Phase 3) — admin-only, unlike
	// every other route above: these create/remove libraries and trigger
	// scans, not just read the catalog.
	s.mux.HandleFunc("GET /Library/VirtualFolders", requireAdmin(s.handleGetVirtualFolders))
	s.mux.HandleFunc("POST /Library/VirtualFolders", requireAdmin(s.handleAddVirtualFolder))
	s.mux.HandleFunc("DELETE /Library/VirtualFolders", requireAdmin(s.handleRemoveVirtualFolder))
	s.mux.HandleFunc("POST /Library/Refresh", requireAdmin(s.handleLibraryRefresh))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// randomID mints an opaque identifier formatted as a canonical UUID
// (formatUUID — see internal/storage/sqlite/id.go's copy of the same
// helper for why the format itself matters, not just uniqueness).
// Stdlib-only (crypto/rand), same approach as
// internal/storage/sqlite's newID/newToken, kept as a separate copy here
// rather than an import since this package must not depend on sqlite
// specifically (it depends only on the storage interface).
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("api: reading random bytes for ID generation: " + err.Error())
	}
	return formatUUID(hex.EncodeToString(b[:]))
}

// formatUUID inserts hyphens into a 32-character hex string at the
// canonical UUID positions (8-4-4-4-12) — see
// internal/storage/sqlite/id.go's copy of this same helper for why real
// Jellyfin clients require it.
func formatUUID(hex32 string) string {
	return hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32]
}
